package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgproto3"
)

// Version 假服务端报告的 PostgreSQL 版本；假 pg_dump / pg_dumpall 报告同一版本，满足“工具大版本不低于服务端”
const Version = "16.4"

// FailingDatabase 假 pg_dump 导出这个库时以非零状态退出（模拟服务端读数据块出错），用于展示失败运行的日志
const FailingDatabase = "broken"

// Database 假服务端列出的库与数据量（字节）
type Database struct {
	Name string
	Size int64
}

// defaultDatabases 未指定时列出的库：一个可以正常导出的业务库、一个导出必然失败的库，以及默认连接库
var defaultDatabases = []Database{
	{Name: "app", Size: 24 << 20},
	{Name: FailingDatabase, Size: 8 << 20},
	{Name: "postgres", Size: 7<<20 + 512<<10},
}

// Config 假服务端接受的账号与列出的库
type Config struct {
	// User、Password 只接受这一组明文密码认证
	User, Password string
	// Databases 为空时使用 defaultDatabases
	Databases []Database
}

// Session 一次认证成功的连接，供测试确认导出工具确实连上了服务端
type Session struct {
	User, Database, Application string
}

// Server 进程内的假 PostgreSQL 服务端：只实现 OpsNap 连接测试、能力探测、库列表与导出前检查用到的查询，
// 其他查询一律返回错误（SQLSTATE 0A000）。不支持 TLS（SSLRequest 回答 N，与未开启 ssl 的服务端相同）。
type Server struct {
	cfg     Config
	ln      net.Listener
	queries map[string]*query

	mu       sync.Mutex
	sessions []Session
	conns    map[net.Conn]struct{}
	closed   bool
	nextPID  uint32
	wg       sync.WaitGroup
}

// Listen 在 addr 上启动假服务端
func Listen(addr string, cfg Config) (*Server, error) {
	if cfg.User == "" {
		return nil, errors.New("fakepg: 需要指定用户名")
	}
	if len(cfg.Databases) == 0 {
		cfg.Databases = defaultDatabases
	}
	dbs := append([]Database(nil), cfg.Databases...)
	sort.Slice(dbs, func(i, j int) bool { return dbs[i].Name < dbs[j].Name })
	cfg.Databases = dbs
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, ln: ln, conns: map[net.Conn]struct{}{}}
	s.queries = s.buildQueries()
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr 实际监听地址
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Port 实际监听端口
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Sessions 至今认证成功的连接
func (s *Server) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Session(nil), s.sessions...)
}

// Close 停止监听并断开所有连接
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = nc.Close()
			return
		}
		s.conns[nc] = struct{}{}
		s.nextPID++
		pid := s.nextPID
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.conns, nc)
				s.mu.Unlock()
				_ = nc.Close()
			}()
			c := &conn{s: s, nc: nc, be: pgproto3.NewBackend(nc, nc), pid: pid,
				stmts: map[string]*query{}, portals: map[string]*portal{}}
			if c.startup() {
				c.serve()
			}
		}()
	}
}

func (s *Server) hasDatabase(name string) bool {
	for _, d := range s.cfg.Databases {
		if d.Name == name {
			return true
		}
	}
	return false
}

// 列类型（pg_type 的 OID）
const (
	oidBool = 16
	oidInt8 = 20
	oidText = 25
)

type column struct {
	name string
	oid  uint32
}

// query 一条支持的查询：结果列、参数个数与按连接生成的结果行（值为 string、int64 或 bool）
type query struct {
	tag    string
	cols   []column
	params int
	rows   func(c *conn) [][]any
}

// normalize 把连续空白压成一个空格，使多行书写的查询与单行形式一致
func normalize(sql string) string { return strings.Join(strings.Fields(sql), " ") }

func rowsOf(v ...any) func(*conn) [][]any {
	return func(*conn) [][]any { return [][]any{v} }
}

// buildQueries OpsNap 发出的查询原文（见 internal/pkg/dsconn/database.go、internal/pkg/probe/postgres.go、
// internal/service/datasource_svc/databases.go、internal/pkg/dump/postgres.go）与假结果
func (s *Server) buildQueries() map[string]*query {
	text := func(name string) []column { return []column{{name, oidText}} }
	qs := map[string]*query{
		"SHOW server_version": {tag: "SHOW", cols: text("server_version"), rows: rowsOf(Version)},
		// 没有 TLS：pg_stat_ssl 中没有本连接的加密记录
		"SELECT COALESCE(version, '') FROM pg_stat_ssl WHERE pid = pg_backend_pid() AND ssl": {
			cols: text("coalesce"), rows: func(*conn) [][]any { return nil },
		},
		"SHOW wal_level":             {tag: "SHOW", cols: text("wal_level"), rows: rowsOf("replica")},
		"SHOW max_wal_senders":       {tag: "SHOW", cols: text("max_wal_senders"), rows: rowsOf("10")},
		"SHOW max_replication_slots": {tag: "SHOW", cols: text("max_replication_slots"), rows: rowsOf("10")},
		"SELECT count(*) FROM pg_replication_slots": {
			cols: []column{{"count", oidInt8}}, rows: rowsOf(int64(0)),
		},
		"SELECT current_user": {
			cols: text("current_user"), rows: func(c *conn) [][]any { return [][]any{{c.user}} },
		},
		"SELECT rolsuper OR rolreplication FROM pg_roles WHERE rolname = current_user": {
			cols: []column{{"?column?", oidBool}}, rows: rowsOf(true),
		},
		`SELECT d.datname,
		CASE WHEN pg_catalog.has_database_privilege(d.datname, 'CONNECT')
			THEN pg_catalog.pg_database_size(d.datname) ELSE 0 END
		FROM pg_catalog.pg_database d
		WHERE d.datistemplate = false AND d.datallowconn = true
		ORDER BY d.datname`: {
			cols: []column{{"datname", oidText}, {"pg_database_size", oidInt8}},
			rows: func(*conn) [][]any {
				out := make([][]any, len(s.cfg.Databases))
				for i, d := range s.cfg.Databases {
					out[i] = []any{d.Name, d.Size}
				}
				return out
			},
		},
		// 排除规则检查：假服务端里没有表，规则都不匹配（OpsNap 只把它写进运行日志）
		"SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace " +
			"WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')": {
			cols: []column{{"count", oidInt8}}, params: 2, rows: rowsOf(int64(0)),
		},
	}
	out := make(map[string]*query, len(qs))
	for sql, q := range qs {
		if q.tag == "" {
			q.tag = "SELECT"
		}
		out[normalize(sql)] = q
	}
	return out
}

func (s *Server) lookup(sql string) (*query, *pgproto3.ErrorResponse) {
	if q, ok := s.queries[normalize(sql)]; ok {
		return q, nil
	}
	return nil, errorResponse("ERROR", "0A000", "fakepg 不支持这条查询: "+normalize(sql))
}

func errorResponse(severity, code, msg string) *pgproto3.ErrorResponse {
	return &pgproto3.ErrorResponse{Severity: severity, SeverityUnlocalized: severity, Code: code, Message: msg}
}

type portal struct {
	q       *query
	formats []int16
}

// conn 一条客户端连接：先完成启动与认证，再处理简单查询与扩展查询协议
type conn struct {
	s    *Server
	nc   net.Conn
	be   *pgproto3.Backend
	pid  uint32
	user string

	stmts   map[string]*query
	portals map[string]*portal
	// failed 扩展协议中出错后，按协议丢弃后续消息直到 Sync
	failed bool
}

func (c *conn) startup() bool {
	for {
		msg, err := c.be.ReceiveStartupMessage()
		if err != nil {
			return false
		}
		switch m := msg.(type) {
		case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
			// 与未开启 ssl 的服务端相同：拒绝加密，客户端按 sslmode 决定是否继续明文
			if _, err := c.nc.Write([]byte{'N'}); err != nil {
				return false
			}
		case *pgproto3.StartupMessage:
			return c.authenticate(m.Parameters["user"], m.Parameters["database"], m.Parameters["application_name"])
		default:
			return false
		}
	}
}

func (c *conn) authenticate(user, database, app string) bool {
	if database == "" {
		database = user
	}
	c.be.Send(&pgproto3.AuthenticationCleartextPassword{})
	if err := c.be.Flush(); err != nil {
		return false
	}
	if err := c.be.SetAuthType(pgproto3.AuthTypeCleartextPassword); err != nil {
		return false
	}
	msg, err := c.be.Receive()
	if err != nil {
		return false
	}
	pm, ok := msg.(*pgproto3.PasswordMessage)
	if !ok || user != c.s.cfg.User || pm.Password != c.s.cfg.Password {
		c.fatal("28P01", fmt.Sprintf("password authentication failed for user %q", user))
		return false
	}
	if !c.s.hasDatabase(database) {
		c.fatal("3D000", fmt.Sprintf("database %q does not exist", database))
		return false
	}
	c.user = user
	c.s.mu.Lock()
	c.s.sessions = append(c.s.sessions, Session{User: user, Database: database, Application: app})
	c.s.mu.Unlock()

	c.be.Send(&pgproto3.AuthenticationOk{})
	for _, kv := range [][2]string{
		{"server_version", Version}, {"server_encoding", "UTF8"}, {"client_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"}, {"integer_datetimes", "on"}, {"standard_conforming_strings", "on"},
		{"TimeZone", "UTC"}, {"application_name", app},
	} {
		c.be.Send(&pgproto3.ParameterStatus{Name: kv[0], Value: kv[1]})
	}
	key := make([]byte, 4)
	binary.BigEndian.PutUint32(key, c.pid*2654435761)
	c.be.Send(&pgproto3.BackendKeyData{ProcessID: c.pid, SecretKey: key})
	c.be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	return c.be.Flush() == nil
}

func (c *conn) fatal(code, msg string) {
	c.be.Send(errorResponse("FATAL", code, msg))
	_ = c.be.Flush()
}

func (c *conn) serve() {
	for {
		msg, err := c.be.Receive()
		if err != nil {
			return
		}
		if _, isSync := msg.(*pgproto3.Sync); c.failed && !isSync {
			continue
		}
		switch m := msg.(type) {
		case *pgproto3.Query:
			c.simpleQuery(m.String)
		case *pgproto3.Parse:
			q, e := c.s.lookup(m.Query)
			if e != nil {
				c.fail(e)
				continue
			}
			c.stmts[m.Name] = q
			c.be.Send(&pgproto3.ParseComplete{})
		case *pgproto3.Describe:
			c.describe(m.ObjectType, m.Name)
		case *pgproto3.Bind:
			q, ok := c.stmts[m.PreparedStatement]
			if !ok {
				c.fail(errorResponse("ERROR", "26000", fmt.Sprintf("prepared statement %q does not exist", m.PreparedStatement)))
				continue
			}
			if len(m.Parameters) != q.params {
				c.fail(errorResponse("ERROR", "08P01", fmt.Sprintf("bind message supplies %d parameters, but prepared statement requires %d", len(m.Parameters), q.params)))
				continue
			}
			c.portals[m.DestinationPortal] = &portal{q: q, formats: append([]int16(nil), m.ResultFormatCodes...)}
			c.be.Send(&pgproto3.BindComplete{})
		case *pgproto3.Execute:
			p, ok := c.portals[m.Portal]
			if !ok {
				c.fail(errorResponse("ERROR", "34000", fmt.Sprintf("portal %q does not exist", m.Portal)))
				continue
			}
			c.sendRows(p.q, p.formats)
		case *pgproto3.Close:
			if m.ObjectType == 'S' {
				delete(c.stmts, m.Name)
			} else {
				delete(c.portals, m.Name)
			}
			c.be.Send(&pgproto3.CloseComplete{})
		case *pgproto3.Sync:
			c.failed = false
			c.portals = map[string]*portal{}
			c.be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		case *pgproto3.Flush:
		case *pgproto3.Terminate:
			return
		default:
			c.fail(errorResponse("ERROR", "08P01", fmt.Sprintf("fakepg 不支持消息 %T", msg)))
		}
		if err := c.be.Flush(); err != nil {
			return
		}
	}
}

func (c *conn) fail(e *pgproto3.ErrorResponse) {
	c.be.Send(e)
	c.failed = true
}

func (c *conn) describe(kind byte, name string) {
	if kind == 'S' {
		q, ok := c.stmts[name]
		if !ok {
			c.fail(errorResponse("ERROR", "26000", fmt.Sprintf("prepared statement %q does not exist", name)))
			return
		}
		oids := make([]uint32, q.params)
		for i := range oids {
			oids[i] = oidText
		}
		c.be.Send(&pgproto3.ParameterDescription{ParameterOIDs: oids})
		c.be.Send(rowDescription(q, nil))
		return
	}
	p, ok := c.portals[name]
	if !ok {
		c.fail(errorResponse("ERROR", "34000", fmt.Sprintf("portal %q does not exist", name)))
		return
	}
	c.be.Send(rowDescription(p.q, p.formats))
}

// simpleQuery 简单查询协议（pgconn 的 Ping 发送注释“-- ping”），结果总是文本格式
func (c *conn) simpleQuery(sql string) {
	defer c.be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if isEmptyQuery(sql) {
		c.be.Send(&pgproto3.EmptyQueryResponse{})
		return
	}
	q, e := c.s.lookup(sql)
	if e != nil {
		c.be.Send(e)
		return
	}
	if q.params > 0 {
		c.be.Send(errorResponse("ERROR", "42P02", "there is no parameter $1"))
		return
	}
	c.be.Send(rowDescription(q, nil))
	c.sendRows(q, nil)
}

// isEmptyQuery 只有空白与 -- 注释的查询
func isEmptyQuery(sql string) bool {
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return false
		}
	}
	return true
}

// format 第 i 列的结果格式：未指定为文本，只有一个时对所有列生效
func format(formats []int16, i int) int16 {
	switch len(formats) {
	case 0:
		return 0
	case 1:
		return formats[0]
	}
	return formats[i]
}

func rowDescription(q *query, formats []int16) *pgproto3.RowDescription {
	fields := make([]pgproto3.FieldDescription, len(q.cols))
	for i, col := range q.cols {
		size := int16(-1)
		switch col.oid {
		case oidInt8:
			size = 8
		case oidBool:
			size = 1
		}
		fields[i] = pgproto3.FieldDescription{Name: []byte(col.name), DataTypeOID: col.oid, DataTypeSize: size,
			TypeModifier: -1, Format: format(formats, i)}
	}
	return &pgproto3.RowDescription{Fields: fields}
}

func (c *conn) sendRows(q *query, formats []int16) {
	rows := q.rows(c)
	for _, row := range rows {
		values := make([][]byte, len(row))
		for i, v := range row {
			values[i] = encode(v, format(formats, i))
		}
		c.be.Send(&pgproto3.DataRow{Values: values})
	}
	tag := q.tag
	if tag == "SELECT" {
		tag += " " + strconv.Itoa(len(rows))
	}
	c.be.Send(&pgproto3.CommandComplete{CommandTag: []byte(tag)})
}

// encode 按文本（0）或二进制（1）格式编码一个值
func encode(v any, f int16) []byte {
	switch x := v.(type) {
	case int64:
		if f == 1 {
			return binary.BigEndian.AppendUint64(nil, uint64(x)) //nolint:gosec // int8 的二进制格式就是补码的大端表示
		}
		return []byte(strconv.FormatInt(x, 10))
	case bool:
		if f == 1 {
			if x {
				return []byte{1}
			}
			return []byte{0}
		}
		if x {
			return []byte("t")
		}
		return []byte("f")
	case string:
		return []byte(x)
	}
	panic(fmt.Sprintf("fakepg: 不支持的值类型 %T", v))
}
