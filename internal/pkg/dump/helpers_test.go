package dump

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"database/sql/driver"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/probe"
)

const testPassword = "s3cret#pw\"x"

// fakeEnv 假工具所在的 PATH、调用记录目录与运行临时目录的父目录
type fakeEnv struct {
	t    *testing.T
	bin  string
	rec  string
	base string
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("导出工具假设类 Unix 环境，与部署目标一致")
	}
	e := &fakeEnv{t: t, bin: t.TempDir(), rec: t.TempDir(), base: filepath.Join(t.TempDir(), "dump")}
	t.Setenv("PATH", e.bin)
	probe.SetToolsDir("")
	return e
}

// tool 写一个假工具：--version 时打印 version；否则记录 argv、环境变量与运行临时目录（连同权限）后执行 body
func (e *fakeEnv) tool(name, version, body string) {
	e.t.Helper()
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\n" +
		"if [ \"$1\" = \"--version\" ]; then printf '%s\\n' '" + version + "'; exit 0; fi\n" +
		"r=" + e.rec + "/" + name + ".$$\n" +
		"printf '%s\\n' \"$@\" > \"$r.argv\"\n" +
		"env > \"$r.env\"\n" +
		"d=\"$HOME\"; [ -d \"$d\" ] && cp -Rp \"$d\" \"$r.dir\"\n" +
		body + "\n"
	require.NoError(e.t, os.WriteFile(filepath.Join(e.bin, name), []byte(script), 0o755)) //nolint:gosec // 测试用假可执行文件
}

// fakeRun 假工具的一次调用记录
type fakeRun struct {
	argv []string
	env  map[string]string
	// dir 调用时运行临时目录的副本
	dir string
}

func (r fakeRun) arg(prefix string) string {
	for _, a := range r.argv {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
	}
	return ""
}

// runs 按 argv 排序返回 name 的所有调用
func (e *fakeEnv) runs(name string) []fakeRun {
	e.t.Helper()
	matches, err := filepath.Glob(filepath.Join(e.rec, name+".*.argv"))
	require.NoError(e.t, err)
	out := make([]fakeRun, 0, len(matches))
	for _, m := range matches {
		stem := strings.TrimSuffix(m, ".argv")
		argv, err := os.ReadFile(m) //nolint:gosec // 测试临时目录
		require.NoError(e.t, err)
		envText, err := os.ReadFile(stem + ".env") //nolint:gosec // 测试临时目录
		require.NoError(e.t, err)
		env := map[string]string{}
		for _, l := range strings.Split(string(envText), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				env[k] = v
			}
		}
		out = append(out, fakeRun{argv: strings.Split(strings.TrimSuffix(string(argv), "\n"), "\n"), env: env, dir: stem + ".dir"})
	}
	sort.Slice(out, func(i, j int) bool { return strings.Join(out[i].argv, " ") < strings.Join(out[j].argv, " ") })
	return out
}

// copied 读取假工具复制下来的运行临时目录中的文件，返回内容与权限
func (r fakeRun) copied(t *testing.T, path string) (string, os.FileMode) {
	t.Helper()
	p := filepath.Join(r.dir, filepath.Base(path))
	b, err := os.ReadFile(p) //nolint:gosec // 测试临时目录
	require.NoError(t, err, path)
	info, err := os.Stat(p)
	require.NoError(t, err)
	return string(b), info.Mode().Perm()
}

// leftEntries base 下剩余的条目
func (e *fakeEnv) leftEntries() []string {
	e.t.Helper()
	ents, err := os.ReadDir(e.base)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(e.t, err)
	var out []string
	for _, x := range ents {
		out = append(out, x.Name())
	}
	return out
}

// noDialer 单元测试中不应经链路拨号的场景
type noDialer struct{}

func (noDialer) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("单元测试不经链路拨号")
}

func (noDialer) DialSSH(context.Context, netchain.Hop) (*ssh.Client, error) {
	return nil, errors.New("单元测试不经链路拨号")
}

func mysqlSource() Source {
	return Source{
		Dialer:        noDialer{},
		Config:        dsconn.Config{Type: dsconn.TypeMySQL, Host: "db.example", Port: 3306, User: "backup", Password: testPassword},
		ServerVersion: "8.0.40",
	}
}

func pgSource() Source {
	return Source{
		Dialer:        noDialer{},
		Config:        dsconn.Config{Type: dsconn.TypePostgreSQL, Host: "pg.example", Port: 5432, User: "backup", Password: testPassword},
		ServerVersion: "16.4 (Debian 16.4-1)",
	}
}

// logs 收集运行日志
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
}

func (l *logs) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// readAll 按 kopia 写快照的方式依次把每个文件读到 EOF
func readAll(t *testing.T, s *Session) (map[string][]byte, error) {
	t.Helper()
	out := map[string][]byte{}
	for _, f := range s.Files() {
		b, err := io.ReadAll(f)
		if err != nil {
			return out, err
		}
		out[f.Name] = b
	}
	return out, nil
}

// ---- 假数据库：按查询文本回答，用于目录与账号查询 ----

type fakeAnswer func(query string, args []driver.Value) (cols []string, rows [][]driver.Value, err error)

type fakeDB struct {
	mu      sync.Mutex
	queries []string
	// dbs 各次打开连接时的 cfg.Database
	dbs    []string
	answer fakeAnswer
}

// install 让 Start 打开的数据库连接都指向 f
func (f *fakeDB) install(t *testing.T) {
	t.Helper()
	old := openDB
	openDB = func(_ context.Context, _ dsconn.Dialer, cfg dsconn.Config) (*sql.DB, error) {
		f.mu.Lock()
		f.dbs = append(f.dbs, cfg.Database)
		f.mu.Unlock()
		return sql.OpenDB(fakeConnector{f}), nil
	}
	t.Cleanup(func() { openDB = old })
}

func (f *fakeDB) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

type fakeConnector struct{ db *fakeDB }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) { return &fakeConn{db: c.db}, nil }
func (c fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("不支持") }

type fakeConn struct{ db *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("不支持预处理") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("不支持事务") }

func (c *fakeConn) ask(query string, named []driver.NamedValue) ([]string, [][]driver.Value, error) {
	args := make([]driver.Value, len(named))
	for i, a := range named {
		args[i] = a.Value
	}
	c.db.mu.Lock()
	c.db.queries = append(c.db.queries, query)
	c.db.mu.Unlock()
	return c.db.answer(query, args)
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	cols, rows, err := c.ask(query, args)
	if err != nil {
		return nil, err
	}
	return &fakeRows{cols: cols, rows: rows}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if _, _, err := c.ask(query, args); err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// ---- TLS 材料 ----

// testCert 生成自签名证书与私钥（PEM）
func testCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "opsnap-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
}
