package datasource_ctr

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	channelapi "github.com/opskat/opsnap/internal/api/channel"
	api "github.com/opskat/opsnap/internal/api/datasource"
	tokenapi "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/controller/channel_ctr"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/probe"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/token_svc"
)

// 测试用假服务的凭据
const (
	adminPassword = "correct-horse-battery"
	sshUser       = "ops"
	sshPassword   = "ssh-pass-7Qe9" //nolint:gosec // 进程内假 SSH 服务端的测试密码
	socksUser     = "proxy"
	socksPassword = "socks-pass-3Wx1" //nolint:gosec // 进程内假 SOCKS5 代理的测试密码
	dbUser        = "backup"
	dbPassword    = "db-pass-5Rt8"    //nolint:gosec // mock 数据库连接的测试密码
	keyPassphrase = "key-phrase-8Lm2" //nolint:gosec // 测试私钥的口令
)

// fakeConnector MySQL / PostgreSQL 的 mock 连接：经链路拨号到数据源地址（验证经由的链路），
// 然后返回预设的结果；服务器文件交给真实实现（连接进程内 SSH 服务端）
type fakeConnector struct {
	mu      sync.Mutex
	info    dsconn.Info
	err     error
	openErr error // 只影响 Open（能力探测用它连接），不影响 Test（先测试后保存用它），用于单独制造“无法探测”
	cfgs    []dsconn.Config
	hook    func() // 下一次 Test 连接进行中调用一次，用来制造并发修改
}

// onTest 下一次 Test 连接进行中调用 fn 一次
func (f *fakeConnector) onTest(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hook = fn
}

func (f *fakeConnector) set(info dsconn.Info, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info, f.err = info, err
}

// setOpenErr 让接下来的 Open（能力探测）失败，不影响先测试后保存的 Test
func (f *fakeConnector) setOpenErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openErr = err
}

func (f *fakeConnector) last() dsconn.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cfgs) == 0 {
		return dsconn.Config{}
	}
	return f.cfgs[len(f.cfgs)-1]
}

func (f *fakeConnector) Test(ctx context.Context, d dsconn.Dialer, cfg dsconn.Config) (dsconn.Info, error) {
	if cfg.Type == dsconn.TypeServerFile {
		return dsconn.Default.Test(ctx, d, cfg)
	}
	f.mu.Lock()
	f.cfgs = append(f.cfgs, cfg)
	info, ferr, hook := f.info, f.err, f.hook
	f.hook = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	conn, err := d.Dial(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		var he *netchain.HopError
		if errors.As(err, &he) {
			return dsconn.Info{}, he
		}
		return dsconn.Info{}, &dsconn.Error{Reason: dsconn.ReasonUnreachable, Msg: err.Error()}
	}
	_ = conn.Close()
	return info, ferr
}

func (f *fakeConnector) Open(ctx context.Context, d dsconn.Dialer, cfg dsconn.Config) (*dsconn.Conn, error) {
	f.mu.Lock()
	openErr := f.openErr
	f.mu.Unlock()
	if openErr != nil {
		return nil, openErr
	}
	info, err := f.Test(ctx, d, cfg)
	if err != nil {
		return nil, err
	}
	return &dsconn.Conn{Info: info}, nil
}

type env struct {
	ctx   context.Context
	mux   *muxtest.TestMux
	token string // API 令牌
	conn  *fakeConnector
}

// fakeProbeOK 默认的假探测：不依赖 conn（真实 probe.Run 需要真实的 DB / SSH 连接，mock 连接给不了），
// 固定返回一项“可用”，只用于不关心探测结果内容的用例；关心探测结果的用例自行 SetProbeRunner
func fakeProbeOK(context.Context, dsconn.Type, *dsconn.Conn) []probe.Item {
	return []probe.Item{{Key: "fake.ok", Title: probe.Text{ZhCN: "示例项", En: "Example"}, Tier: probe.TierOK,
		Detail: probe.Text{ZhCN: "正常", En: "OK"}}}
}

func setupTest(t *testing.T) *env {
	ctx := testdb.New(t)
	datasource_svc.SetProbeRunner(fakeProbeOK)
	// t.Cleanup 按后注册先执行：gogo.Wait 必须先跑完所有在途的后台探测，再把 runner 换回真实的 probe.Run，
	// 否则一个仍在跑的后台探测会在 runner 被换掉之后才读到它，对着 mock 连接跑真实探测而崩溃；
	// 也要先于 testdb 释放数据库连接，否则残留的后台探测会在关闭后的数据库上出错（-race 下更明显）
	t.Cleanup(func() { datasource_svc.SetProbeRunner(nil) })
	t.Cleanup(gogo.Wait)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	token_repo.RegisterToken(token_repo.NewToken())
	channel_repo.RegisterChannel(channel_repo.NewChannel())
	datasource_repo.RegisterDataSource(datasource_repo.NewDataSource())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	datasource_svc.RegisterChannelHooks()
	conn := &fakeConnector{info: dsconn.Info{Version: "8.0.36", TLS: &dsconn.TLSInfo{Version: "TLSv1.3"}}}
	datasource_svc.SetConnector(conn)
	t.Cleanup(func() { datasource_svc.SetConnector(nil) })

	setupCode, _ := auth_svc.Auth().PrepareSetupCode(ctx)
	_, _, err = auth_svc.Auth().Setup(ctx, &authapi.SetupRequest{SetupCode: setupCode, Username: "admin", Password: adminPassword},
		auth_svc.ClientMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	tok, err := token_svc.Token().Create(ctx, &tokenapi.CreateRequest{Name: "ci"})
	require.NoError(t, err)

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	ctr := NewDataSource()
	ch := channel_ctr.NewChannel()
	authed := testMux.Group("/api/v1", middleware.SameOrigin()).Group("/", middleware.Auth())
	authed.Bind(ctr.List, ctr.Get, ctr.Probe, ctr.Create, ctr.Update, ctr.Test, ctr.ConfirmHostKey, ctr.Delete, ctr.Reprobe,
		ch.List, ch.Create, ch.Update, ch.Test, ch.ConfirmHostKey, ch.Delete)
	return &env{ctx: ctx, mux: testMux, token: tok.Token, conn: conn}
}

// do 用 API 令牌调用
func (e *env) do(req, resp any) error {
	return e.mux.Do(e.ctx, req, resp, muxclient.WithHeader(http.Header{"Authorization": {"Bearer " + e.token}}))
}

func errCode(err error) int {
	var he *httputils.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, p, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	return host, port
}

// newDB 只接受连接的 TCP 服务，代表 MySQL / PostgreSQL 的地址（协议由 mock 连接代替）
func newDB(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

// testKey 生成 Ed25519 私钥，passphrase 非空时加密；返回 OpenSSH 格式私钥与公钥
func testKey(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(block)), sshPub
}

// testCert 自签名证书与私钥（PEM）
func testCert(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "opsnap test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	kder, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
}

func newSSH(t *testing.T, keys ...ssh.PublicKey) *fakessh.Server {
	t.Helper()
	srv, err := fakessh.Listen("127.0.0.1:0", fakessh.Config{User: sshUser, Password: sshPassword, AuthorizedKeys: keys})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func newSOCKS5(t *testing.T) *fakessh.SOCKS5 {
	t.Helper()
	p, err := fakessh.ListenSOCKS5("127.0.0.1:0", fakessh.SOCKS5Config{User: socksUser, Password: socksPassword})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func mysqlForm(t *testing.T, name, addr string) api.Form {
	host, port := hostPort(t, addr)
	return api.Form{Name: name, Kind: "mysql", Host: host, Port: port, Username: dbUser, Password: dbPassword}
}

func serverForm(t *testing.T, name string, srv *fakessh.Server) api.Form {
	host, port := hostPort(t, srv.Addr())
	return api.Form{Name: name, Kind: "server_file", Host: host, Port: port, Username: sshUser, AuthMethod: "password", Password: sshPassword}
}

// createChannel 新建通道；SSH 通道信任出示的主机密钥
func (e *env) createChannel(t *testing.T, f channelapi.Form) *channelapi.Item {
	t.Helper()
	resp := &channelapi.CreateResponse{}
	require.NoError(t, e.do(&channelapi.CreateRequest{Channel: f}, resp))
	if resp.HostKey != nil {
		f.HostKey = resp.HostKey.Fingerprint
		resp = &channelapi.CreateResponse{}
		require.NoError(t, e.do(&channelapi.CreateRequest{Channel: f}, resp))
	}
	require.NotNil(t, resp.Item)
	return resp.Item
}

func (e *env) sshChannel(t *testing.T, name string, srv *fakessh.Server, viaID int64) *channelapi.Item {
	host, port := hostPort(t, srv.Addr())
	return e.createChannel(t, channelapi.Form{Name: name, Kind: "ssh", Host: host, Port: port, Username: sshUser,
		AuthMethod: "password", Password: sshPassword, ViaID: viaID})
}

func (e *env) socksChannel(t *testing.T, name string, p *fakessh.SOCKS5) *channelapi.Item {
	host, port := hostPort(t, p.Addr())
	return e.createChannel(t, channelapi.Form{Name: name, Kind: "socks5", Host: host, Port: port, Username: socksUser, Password: socksPassword})
}

// create 新建数据源；需要确认目标主机密钥时信任出示的指纹后重试
func (e *env) create(t *testing.T, f api.Form) *api.Item {
	t.Helper()
	resp := &api.CreateResponse{}
	require.NoError(t, e.do(&api.CreateRequest{DataSource: f}, resp))
	if resp.HostKey != nil {
		f.HostKey = resp.HostKey.Fingerprint
		resp = &api.CreateResponse{}
		require.NoError(t, e.do(&api.CreateRequest{DataSource: f}, resp))
	}
	require.Nil(t, resp.HostKey)
	require.NotNil(t, resp.Item)
	return resp.Item
}

func (e *env) list(t *testing.T) []*api.Item {
	t.Helper()
	resp := &api.ListResponse{}
	require.NoError(t, e.do(&api.ListRequest{}, resp))
	return resp.Items
}

func (e *env) get(t *testing.T, id int64) *api.Item {
	t.Helper()
	resp := &api.GetResponse{}
	require.NoError(t, e.do(&api.GetRequest{ID: id}, resp))
	return resp.Item
}

func (e *env) channel(t *testing.T, id int64) *channelapi.Item {
	t.Helper()
	resp := &channelapi.ListResponse{}
	require.NoError(t, e.do(&channelapi.ListRequest{}, resp))
	for _, it := range resp.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("通道 %d 不在列表中", id)
	return nil
}

func (e *env) row(t *testing.T, id int64) *datasource_entity.DataSource {
	t.Helper()
	var row datasource_entity.DataSource
	require.NoError(t, db.Ctx(e.ctx).First(&row, id).Error)
	return &row
}

func (e *env) decrypt(t *testing.T, ct string) string {
	t.Helper()
	pt, err := secret_svc.Secret().Decrypt(e.ctx, ct)
	require.NoError(t, err)
	return pt
}

func chainNames(hops []*channelapi.Hop) []string {
	names := make([]string, 0, len(hops))
	for _, h := range hops {
		names = append(names, h.Name)
	}
	return names
}

func TestCreate(t *testing.T) {
	convey.Convey("新建数据源", t, func() {
		e := setupTest(t)
		dbAddr := newDB(t)

		convey.Convey("MySQL 直连：测试成功后保存，记录版本与 TLS 状态，秘密只以密文保存且不返回", func() {
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{DataSource: mysqlForm(t, "orders", dbAddr)}, resp))
			require.NotNil(t, resp.Item)
			assert.Nil(t, resp.HostKey)
			it := resp.Item
			assert.Equal(t, datasource_entity.StatusOK, it.Status)
			assert.Equal(t, "mysql", it.Kind)
			assert.Equal(t, "mysql://"+dbAddr, it.Address)
			assert.Equal(t, "prefer", it.TLSMode)
			assert.True(t, it.HasPassword)
			assert.Equal(t, []string{"orders"}, chainNames(it.Chain))
			require.NotNil(t, it.Server)
			assert.Equal(t, "8.0.36", it.Server.Version)
			require.NotNil(t, it.Server.TLS)
			assert.Equal(t, "TLSv1.3", it.Server.TLS.Version)
			assert.False(t, it.Server.TLS.Verified)
			assert.NotZero(t, it.CheckedAt)

			cfg := e.conn.last()
			assert.Equal(t, dsconn.TypeMySQL, cfg.Type)
			assert.Equal(t, dbPassword, cfg.Password)
			assert.Equal(t, dsconn.TLSPrefer, cfg.TLS.Mode)

			row := e.row(t, it.ID)
			assert.NotContains(t, row.Password, dbPassword, "数据库中的密码是密文")
			assert.Equal(t, dbPassword, e.decrypt(t, row.Password))

			var raw map[string]any
			require.NoError(t, e.do(&api.ListRequest{}, &raw))
			body, _ := json.Marshal(raw)
			assert.NotContains(t, string(body), dbPassword, "接口不返回秘密")
			assert.Contains(t, string(body), "orders")
			assert.Equal(t, it.ID, e.get(t, it.ID).ID)
		})

		convey.Convey("默认端口与 PostgreSQL 的默认连接数据库", func() {
			e.conn.set(dsconn.Info{Version: "16.4"}, nil)
			for _, c := range []struct {
				kind string
				port int
			}{{"mysql", 3306}, {"postgres", 5432}} {
				// 默认端口上没有服务：只检查传给连接的参数
				f := api.Form{Name: c.kind, Kind: c.kind, Host: "127.0.0.1", Username: dbUser, Password: dbPassword}
				_ = e.do(&api.ProbeRequest{DataSource: f}, &api.ProbeResponse{})
				assert.Equal(t, c.port, e.conn.last().Port)
			}
			assert.Equal(t, "postgres", e.conn.last().Database)

			host, port := hostPort(t, dbAddr)
			item := e.create(t, api.Form{Name: "pg", Kind: "postgres", Host: host, Port: port, Username: dbUser, Password: dbPassword, TLSMode: "disable"})
			assert.Equal(t, "postgres", item.Database)
			assert.Equal(t, "disable", item.TLSMode)
			assert.Equal(t, "postgres://"+dbAddr, item.Address)
			assert.Nil(t, item.Server.TLS)
			assert.Equal(t, "16.4", item.Server.Version)
		})

		convey.Convey("测试失败时按原文显示原因、指出第几跳，并且不保存", func() {
			e.conn.set(dsconn.Info{}, &dsconn.Error{Reason: dsconn.ReasonAuthFailed, Msg: "Error 1045: Access denied for user 'backup'"})
			err := e.do(&api.CreateRequest{DataSource: mysqlForm(t, "orders", dbAddr)}, &api.CreateResponse{})
			assert.Equal(t, code.DataSourceTestFailed, errCode(err))
			assert.Contains(t, err.Error(), "第 1 跳 orders（MySQL）：认证失败（Error 1045: Access denied for user 'backup'）")
			assert.NotContains(t, err.Error(), dbPassword)
			assert.Empty(t, e.list(t))

			e.conn.set(dsconn.Info{}, &dsconn.Error{Reason: dsconn.ReasonCertificate, Msg: "x509: certificate signed by unknown authority"})
			err = e.do(&api.ProbeRequest{DataSource: mysqlForm(t, "orders", dbAddr)}, &api.ProbeResponse{})
			assert.Contains(t, err.Error(), "证书校验失败（x509: certificate signed by unknown authority）")
		})

		convey.Convey("字段校验", func() {
			certPEM, keyPEM := testCert(t)
			_, otherKey := testCert(t)
			my := func(mod func(*api.Form)) api.Form {
				f := mysqlForm(t, "a", dbAddr)
				mod(&f)
				return f
			}
			sf := func(mod func(*api.Form)) api.Form {
				f := api.Form{Name: "s", Kind: "server_file", Host: "127.0.0.1", Username: sshUser, AuthMethod: "password", Password: "x"}
				mod(&f)
				return f
			}
			encKey, _ := testKey(t, keyPassphrase)
			cases := []struct {
				form api.Form
				want int
			}{
				{my(func(f *api.Form) { f.Name = " " }), code.DataSourceNameInvalid},
				{my(func(f *api.Form) { f.Name = strings.Repeat("名", 65) }), code.DataSourceNameInvalid},
				{my(func(f *api.Form) { f.Host = " " }), code.DataSourceHostRequired},
				{my(func(f *api.Form) { f.Port = 65536 }), code.DataSourcePortInvalid},
				{my(func(f *api.Form) { f.Port = -1 }), code.DataSourcePortInvalid},
				{my(func(f *api.Form) { f.Username = "" }), code.DataSourceUserRequired},
				{my(func(f *api.Form) { f.Password = "" }), code.DataSourcePasswordRequired},
				{my(func(f *api.Form) { f.TLSClientCert = certPEM }), code.DataSourceTLSPairRequired},
				{my(func(f *api.Form) { f.TLSClientKey = keyPEM }), code.DataSourceTLSPairRequired},
				{my(func(f *api.Form) { f.TLSMode = "verify_ca"; f.TLSCA = "not a cert" }), code.DataSourceCAInvalid},
				{my(func(f *api.Form) { f.TLSClientCert, f.TLSClientKey = "bad", keyPEM }), code.DataSourceClientCertInvalid},
				{my(func(f *api.Form) { f.TLSClientCert, f.TLSClientKey = certPEM, "bad" }), code.DataSourceClientKeyInvalid},
				{my(func(f *api.Form) { f.TLSClientCert, f.TLSClientKey = certPEM, otherKey }), code.DataSourceClientKeyInvalid},
				{my(func(f *api.Form) { f.ChannelID = 999 }), code.DataSourceChannelNotFound},
				{sf(func(f *api.Form) { f.AuthMethod = "" }), code.DataSourceAuthMethodInvalid},
				{sf(func(f *api.Form) { f.Password = "" }), code.DataSourcePasswordRequired},
				{sf(func(f *api.Form) { f.AuthMethod = "key" }), code.DataSourcePrivateKeyRequired},
				{sf(func(f *api.Form) { f.AuthMethod, f.PrivateKey = "key", "not a key" }), code.DataSourceKeyInvalid},
				{sf(func(f *api.Form) { f.AuthMethod, f.PrivateKey = "key", encKey }), code.DataSourcePassphraseMissing},
				{sf(func(f *api.Form) { f.AuthMethod, f.PrivateKey, f.Passphrase = "key", encKey, "wrong" }), code.DataSourcePassphraseWrong},
			}
			for _, c := range cases {
				assert.Equal(t, c.want, errCode(e.do(&api.CreateRequest{DataSource: c.form}, &api.CreateResponse{})), "%+v", c.form)
				assert.Equal(t, c.want, errCode(e.do(&api.ProbeRequest{DataSource: c.form}, &api.ProbeResponse{})), "%+v", c.form)
			}
			assert.Empty(t, e.conn.cfgs, "字段错误时不联网")
			assert.Empty(t, e.list(t))
			err := e.do(&api.CreateRequest{DataSource: my(func(f *api.Form) { f.Kind = "redis" })}, &api.CreateResponse{})
			assert.Error(t, err)
		})

		convey.Convey("名称不能与已有数据源重复；同一地址可以被多个数据源使用", func() {
			e.create(t, mysqlForm(t, "orders", dbAddr))
			err := e.do(&api.CreateRequest{DataSource: mysqlForm(t, "orders", dbAddr)}, &api.CreateResponse{})
			assert.Equal(t, code.DataSourceNameDuplicate, errCode(err))
			e.create(t, mysqlForm(t, "orders-readonly", dbAddr))
			assert.Len(t, e.list(t), 2)
		})

		convey.Convey("mTLS：客户端私钥加密保存，证书原样返回", func() {
			certPEM, keyPEM := testCert(t)
			f := mysqlForm(t, "orders", dbAddr)
			f.TLSMode, f.TLSCA, f.TLSClientCert, f.TLSClientKey = "verify_full", certPEM, certPEM, keyPEM
			e.conn.set(dsconn.Info{Version: "8.0.36", TLS: &dsconn.TLSInfo{Version: "TLSv1.3", Verified: true}}, nil)
			item := e.create(t, f)
			assert.True(t, item.HasTLSClientKey)
			assert.Equal(t, certPEM, item.TLSClientCert)
			assert.Equal(t, certPEM, item.TLSCA)
			assert.True(t, item.Server.TLS.Verified)
			assert.Equal(t, keyPEM, string(e.conn.last().TLS.ClientKey))
			row := e.row(t, item.ID)
			assert.NotContains(t, row.TLSClientKey, "PRIVATE KEY")
			assert.Equal(t, keyPEM, e.decrypt(t, row.TLSClientKey))
		})

		convey.Convey("服务器文件：首次连接暂停在主机密钥确认，不认证、不保存；信任后执行 uname -sm 并保存", func() {
			srv := newSSH(t)
			f := serverForm(t, "web-01", srv)
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{DataSource: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.Nil(t, resp.Item)
			assert.Equal(t, 1, resp.HostKey.Hop)
			assert.Equal(t, "web-01", resp.HostKey.Name)
			assert.Equal(t, srv.Addr(), resp.HostKey.Address)
			assert.Equal(t, srv.Fingerprint(), resp.HostKey.Fingerprint)
			assert.False(t, resp.HostKey.Changed)
			assert.Zero(t, srv.AuthAttempts(), "主机密钥未确认时不发送任何凭据")
			assert.Empty(t, e.list(t))

			f.HostKey = resp.HostKey.Fingerprint
			resp = &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{DataSource: f}, resp))
			require.NotNil(t, resp.Item)
			it := resp.Item
			assert.Equal(t, datasource_entity.StatusOK, it.Status)
			assert.Equal(t, "Linux x86_64", it.Server.System)
			assert.Equal(t, srv.Fingerprint(), it.HostKey)
			assert.Equal(t, "ssh://"+sshUser+"@"+srv.Addr(), it.Address)
			assert.Equal(t, "password", it.AuthMethod)
			assert.Empty(t, it.TLSMode)
			assert.Contains(t, srv.Commands(), "uname -sm")
			assert.Equal(t, sshPassword, e.decrypt(t, e.row(t, it.ID).Password))
		})

		convey.Convey("服务器文件：出示的密钥与信任的指纹不一致时不认证、不保存", func() {
			srv := newSSH(t)
			f := serverForm(t, "web-01", srv)
			f.HostKey = "SHA256:not-this-one"
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{DataSource: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.True(t, resp.HostKey.Changed)
			assert.Equal(t, "SHA256:not-this-one", resp.HostKey.Saved)
			assert.Zero(t, srv.AuthAttempts())
			assert.Empty(t, e.list(t))
		})

		convey.Convey("服务器文件：认证失败指出第几跳", func() {
			srv := newSSH(t)
			f := serverForm(t, "web-01", srv)
			f.HostKey, f.Password = srv.Fingerprint(), "wrong"
			err := e.do(&api.CreateRequest{DataSource: f}, &api.CreateResponse{})
			assert.Contains(t, err.Error(), "第 1 跳 web-01（SSH）：认证失败")
			assert.Empty(t, e.list(t))
		})

		convey.Convey("服务器文件：私钥认证（含口令）", func() {
			key, pub := testKey(t, keyPassphrase)
			srv := newSSH(t, pub)
			f := serverForm(t, "web-01", srv)
			f.AuthMethod, f.Password, f.PrivateKey, f.Passphrase = "key", "", key, keyPassphrase
			item := e.create(t, f)
			assert.Equal(t, "key", item.AuthMethod)
			assert.True(t, item.HasPrivateKey)
			assert.True(t, item.HasPassphrase)
			assert.False(t, item.HasPassword)
			row := e.row(t, item.ID)
			assert.Equal(t, key, e.decrypt(t, row.PrivateKey))
			assert.Equal(t, keyPassphrase, e.decrypt(t, row.Passphrase))
		})

		convey.Convey("经 SOCKS5 → SSH 连接 MySQL：链路按顺序显示，最后一跳是数据源", func() {
			proxy := newSOCKS5(t)
			bastion := newSSH(t)
			socks := e.socksChannel(t, "office-socks", proxy)
			jump := e.sshChannel(t, "bastion-prod", bastion, socks.ID)
			f := mysqlForm(t, "orders", dbAddr)
			f.ChannelID = jump.ID

			probe := &api.ProbeResponse{}
			require.NoError(t, e.do(&api.ProbeRequest{DataSource: f}, probe))
			assert.Equal(t, []string{"office-socks", "bastion-prod", "orders"}, chainNames(probe.Chain))
			assert.Equal(t, "8.0.36", probe.Server.Version)
			assert.Empty(t, e.list(t), "测试连接不保存")

			item := e.create(t, f)
			assert.Equal(t, jump.ID, item.ChannelID)
			assert.Equal(t, []string{"office-socks", "bastion-prod", "orders"}, chainNames(item.Chain))
			assert.Contains(t, proxy.Requests(), bastion.Addr())
			assert.Contains(t, bastion.Forwards(), dbAddr)

			convey.Convey("链路中某一跳失败时指出是第几跳哪个通道", func() {
				require.NoError(t, proxy.Close())
				err := e.do(&api.ProbeRequest{ID: item.ID, DataSource: f}, &api.ProbeResponse{})
				assert.Equal(t, code.ChannelHopFailed, errCode(err))
				assert.Contains(t, err.Error(), "第 1 跳 office-socks（SOCKS5）：无法连接")
			})
		})

		convey.Convey("服务器文件经 SSH 跳板：目标主机是链路之后的一跳", func() {
			bastion := newSSH(t)
			target := newSSH(t)
			jump := e.sshChannel(t, "bastion-prod", bastion, 0)
			f := serverForm(t, "web-01", target)
			f.ChannelID = jump.ID
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{DataSource: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.Equal(t, 2, resp.HostKey.Hop)
			assert.Equal(t, target.Fingerprint(), resp.HostKey.Fingerprint)
			item := e.create(t, f)
			assert.Equal(t, []string{"bastion-prod", "web-01"}, chainNames(item.Chain))
			assert.Contains(t, bastion.Forwards(), target.Addr())
		})
	})
}

func TestUpdate(t *testing.T) {
	convey.Convey("编辑数据源", t, func() {
		e := setupTest(t)
		dbAddr := newDB(t)
		certPEM, keyPEM := testCert(t)
		f := mysqlForm(t, "orders", dbAddr)
		f.TLSClientCert, f.TLSClientKey = certPEM, keyPEM
		item := e.create(t, f)
		before := e.row(t, item.ID)

		convey.Convey("秘密留空表示沿用", func() {
			f.Name, f.Password, f.TLSClientKey = "orders-2", "", ""
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, DataSource: f}, resp))
			assert.Equal(t, "orders-2", resp.Item.Name)
			assert.Equal(t, dbPassword, e.conn.last().Password)
			assert.Equal(t, keyPEM, string(e.conn.last().TLS.ClientKey))
			row := e.row(t, item.ID)
			assert.Equal(t, dbPassword, e.decrypt(t, row.Password))
			assert.Equal(t, keyPEM, e.decrypt(t, row.TLSClientKey))

			convey.Convey("去掉客户端证书时一并删除客户端私钥", func() {
				f.TLSClientCert = ""
				resp := &api.UpdateResponse{}
				require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, DataSource: f}, resp))
				assert.False(t, resp.Item.HasTLSClientKey)
				assert.Empty(t, e.row(t, item.ID).TLSClientKey)
			})
		})

		convey.Convey("测试失败时保持编辑前的全部设置", func() {
			e.conn.set(dsconn.Info{}, &dsconn.Error{Reason: dsconn.ReasonAuthFailed, Msg: "Access denied"})
			f.Name, f.Password = "renamed", "wrong"
			err := e.do(&api.UpdateRequest{ID: item.ID, DataSource: f}, &api.UpdateResponse{})
			assert.Equal(t, code.DataSourceTestFailed, errCode(err))
			assert.Equal(t, before, e.row(t, item.ID))
		})

		convey.Convey("名称不能与其他数据源重复，可以保留自己的名称", func() {
			other := e.create(t, mysqlForm(t, "other", dbAddr))
			err := e.do(&api.UpdateRequest{ID: other.ID, DataSource: mysqlForm(t, "orders", dbAddr)}, &api.UpdateResponse{})
			assert.Equal(t, code.DataSourceNameDuplicate, errCode(err))
			require.NoError(t, e.do(&api.UpdateRequest{ID: other.ID, DataSource: mysqlForm(t, "other", dbAddr)}, &api.UpdateResponse{}))
		})

		convey.Convey("服务器文件：切换为私钥认证后原来的密码被删除；换了主机按首次连接确认", func() {
			key, pub := testKey(t, "")
			srv := newSSH(t, pub)
			sf := serverForm(t, "web-01", srv)
			server := e.create(t, sf)

			sf.AuthMethod, sf.Password, sf.PrivateKey, sf.HostKey = "key", "", key, ""
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: server.ID, DataSource: sf}, resp))
			require.NotNil(t, resp.Item, "主机未变时沿用确认过的主机密钥")
			assert.Equal(t, "key", resp.Item.AuthMethod)
			assert.False(t, resp.Item.HasPassword)
			assert.Empty(t, e.row(t, server.ID).Password)

			srv2 := newSSH(t, pub)
			host, port := hostPort(t, srv2.Addr())
			sf.Host, sf.Port = host, port
			resp = &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: server.ID, DataSource: sf}, resp))
			require.NotNil(t, resp.HostKey)
			assert.Equal(t, srv2.Fingerprint(), resp.HostKey.Fingerprint)
			assert.Equal(t, srv.Fingerprint(), e.row(t, server.ID).HostKey, "未确认前不保存")
		})

		convey.Convey("数据源不存在", func() {
			err := e.do(&api.UpdateRequest{ID: 999, DataSource: f}, &api.UpdateResponse{})
			assert.Equal(t, code.DataSourceNotFound, errCode(err))
			err = e.do(&api.GetRequest{ID: 999}, &api.GetResponse{})
			assert.Equal(t, code.DataSourceNotFound, errCode(err))
		})
	})
}

func TestTest(t *testing.T) {
	convey.Convey("测试已保存的数据源", t, func() {
		e := setupTest(t)
		dbAddr := newDB(t)
		item := e.create(t, mysqlForm(t, "orders", dbAddr))

		convey.Convey("失败时记录原因与时间，恢复后变回正常", func() {
			e.conn.set(dsconn.Info{}, &dsconn.Error{Reason: dsconn.ReasonTimeout, Msg: "连接超时: i/o timeout"})
			resp := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: item.ID}, resp))
			assert.Equal(t, datasource_entity.StatusUnreachable, resp.Item.Status)
			assert.Equal(t, "第 1 跳 orders（MySQL）：连接超时", resp.Item.StatusMessage)
			require.NotNil(t, resp.Item.FailedHop)
			assert.Zero(t, resp.Item.FailedHop.ChannelID)
			assert.NotZero(t, resp.Item.CheckedAt)
			assert.Equal(t, "8.0.36", resp.Item.Server.Version, "保留最近一次读取的版本")
			assert.Equal(t, datasource_entity.StatusUnreachable, e.get(t, item.ID).Status)

			e.conn.set(dsconn.Info{Version: "8.0.37"}, nil)
			resp = &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: item.ID}, resp))
			assert.Equal(t, datasource_entity.StatusOK, resp.Item.Status)
			assert.Empty(t, resp.Item.StatusMessage)
			assert.Nil(t, resp.Item.FailedHop)
			assert.Equal(t, "8.0.37", resp.Item.Server.Version)
			assert.Nil(t, resp.Item.Server.TLS)
		})

		convey.Convey("测试期间被编辑的设置与刚落库的探测结果不会被测试结果覆盖", func() {
			done := make(chan int64, 4)
			datasource_svc.SetProbeDoneHook(func(id int64) { done <- id })
			t.Cleanup(func() { datasource_svc.SetProbeDoneHook(nil) })
			fresh := e.create(t, mysqlForm(t, "ledger", dbAddr))
			waitProbe(t, done, fresh.ID)
			e.conn.onTest(func() {
				require.NoError(t, db.Ctx(e.ctx).Model(&datasource_entity.DataSource{}).
					Where("id = ?", fresh.ID).Update("name", "ledger-v2").Error)
			})
			require.NoError(t, e.do(&api.TestRequest{ID: fresh.ID}, &api.TestResponse{}))
			got := e.get(t, fresh.ID)
			assert.Equal(t, "ledger-v2", got.Name)
			require.NotNil(t, got.Probe)
			assert.Equal(t, "done", got.Probe.State)
		})

		convey.Convey("数据源不存在", func() {
			err := e.do(&api.TestRequest{ID: 999}, &api.TestResponse{})
			assert.Equal(t, code.DataSourceNotFound, errCode(err))
		})
	})
}

func TestHostKey(t *testing.T) {
	convey.Convey("主机密钥变化与重新确认", t, func() {
		e := setupTest(t)

		convey.Convey("目标主机的密钥变化：中止连接不认证，状态变为主机密钥已变化", func() {
			srv := newSSH(t)
			item := e.create(t, serverForm(t, "web-01", srv))
			old := srv.Fingerprint()
			require.NoError(t, srv.RotateHostKey())
			attempts := srv.AuthAttempts()

			resp := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: item.ID}, resp))
			assert.Equal(t, datasource_entity.StatusHostKeyChanged, resp.Item.Status)
			assert.Equal(t, srv.Fingerprint(), resp.Item.PresentedHostKey)
			assert.Contains(t, resp.Item.StatusMessage, "第 1 跳 web-01（SSH）：主机密钥已变化")
			require.NotNil(t, resp.Item.FailedHop)
			assert.Zero(t, resp.Item.FailedHop.ChannelID)
			require.NotNil(t, resp.HostKey)
			assert.True(t, resp.HostKey.Changed)
			assert.Equal(t, old, resp.HostKey.Saved)
			assert.Equal(t, attempts, srv.AuthAttempts())

			convey.Convey("重新确认：指纹与出示的不一致时不保存", func() {
				cr := &api.ConfirmHostKeyResponse{}
				require.NoError(t, e.do(&api.ConfirmHostKeyRequest{ID: item.ID, Fingerprint: "SHA256:other"}, cr))
				assert.Nil(t, cr.Item)
				require.NotNil(t, cr.HostKey)
				assert.Equal(t, old, cr.HostKey.Saved)
				assert.Equal(t, srv.Fingerprint(), cr.HostKey.Fingerprint)
				assert.Equal(t, old, e.row(t, item.ID).HostKey)
				assert.Equal(t, attempts, srv.AuthAttempts())
			})

			convey.Convey("重新确认：信任新密钥后保存并重新测试", func() {
				cr := &api.ConfirmHostKeyResponse{}
				require.NoError(t, e.do(&api.ConfirmHostKeyRequest{ID: item.ID, Fingerprint: srv.Fingerprint()}, cr))
				require.NotNil(t, cr.Item)
				assert.Equal(t, datasource_entity.StatusOK, cr.Item.Status)
				assert.Equal(t, srv.Fingerprint(), cr.Item.HostKey)
				assert.Empty(t, cr.Item.PresentedHostKey)
				assert.Greater(t, srv.AuthAttempts(), attempts)
			})
		})

		convey.Convey("重新确认：空白指纹被拒绝，不会清掉已信任的密钥", func() {
			srv := newSSH(t)
			item := e.create(t, serverForm(t, "web-02", srv))
			saved := e.row(t, item.ID).HostKey
			require.NotEmpty(t, saved)
			err := e.do(&api.ConfirmHostKeyRequest{ID: item.ID, Fingerprint: " "}, &api.ConfirmHostKeyResponse{})
			assert.Error(t, err)
			assert.Equal(t, saved, e.row(t, item.ID).HostKey)
		})

		convey.Convey("MySQL 数据源没有目标主机密钥", func() {
			item := e.create(t, mysqlForm(t, "orders", newDB(t)))
			err := e.do(&api.ConfirmHostKeyRequest{ID: item.ID, Fingerprint: "SHA256:x"}, &api.ConfirmHostKeyResponse{})
			assert.Equal(t, code.DataSourceNotServerFile, errCode(err))
		})

		convey.Convey("链路中通道的密钥变化：数据源与通道都标为主机密钥已变化并指明是哪个通道", func() {
			bastion := newSSH(t)
			inner := newSSH(t)
			dbAddr := newDB(t)
			jump := e.sshChannel(t, "bastion-prod", bastion, 0)
			jump2 := e.sshChannel(t, "inner", inner, jump.ID)
			f := mysqlForm(t, "orders", dbAddr)
			f.ChannelID = jump.ID
			direct := e.create(t, f)
			f2 := mysqlForm(t, "orders-deep", dbAddr)
			f2.ChannelID = jump2.ID
			deep := e.create(t, f2)
			other := e.create(t, mysqlForm(t, "elsewhere", dbAddr))
			require.NoError(t, bastion.RotateHostKey())
			attempts, forwards := bastion.AuthAttempts(), len(bastion.Forwards())

			resp := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: direct.ID}, resp))
			assert.Equal(t, datasource_entity.StatusHostKeyChanged, resp.Item.Status)
			assert.Contains(t, resp.Item.StatusMessage, "第 1 跳 bastion-prod（SSH）：主机密钥已变化")
			require.NotNil(t, resp.Item.FailedHop)
			assert.Equal(t, jump.ID, resp.Item.FailedHop.ChannelID)
			assert.Nil(t, resp.HostKey, "变化的是通道，在通道上重新确认")
			assert.Empty(t, resp.Item.PresentedHostKey)
			assert.Equal(t, attempts, bastion.AuthAttempts())
			assert.Len(t, bastion.Forwards(), forwards, "通道的主机密钥变化时不经它转发")
			ch := e.channel(t, jump.ID)
			assert.Equal(t, channel_entity.StatusHostKeyChanged, ch.Status)
			assert.Equal(t, bastion.Fingerprint(), ch.PresentedHostKey)

			err := e.do(&api.ProbeRequest{ID: direct.ID, DataSource: f}, &api.ProbeResponse{})
			assert.Equal(t, code.ChannelHostKeyChanged, errCode(err))

			convey.Convey("测试通道发现密钥变化时，经过它的数据源同样标为主机密钥已变化", func() {
				otherBefore := e.row(t, other.ID)
				require.NoError(t, e.do(&channelapi.TestRequest{ID: jump.ID}, &channelapi.TestResponse{}))
				got := e.get(t, deep.ID)
				assert.Equal(t, datasource_entity.StatusHostKeyChanged, got.Status)
				assert.Contains(t, got.StatusMessage, "第 1 跳 bastion-prod（SSH）：主机密钥已变化")
				require.NotNil(t, got.FailedHop)
				assert.Equal(t, jump.ID, got.FailedHop.ChannelID)
				assert.Equal(t, otherBefore, e.row(t, other.ID), "不经过该通道的数据源不受影响")
			})

			convey.Convey("在通道上信任新密钥后，重新测试该通道以及所有经过它的数据源", func() {
				require.NoError(t, e.do(&api.TestRequest{ID: deep.ID}, &api.TestResponse{}))
				assert.Equal(t, datasource_entity.StatusHostKeyChanged, e.get(t, deep.ID).Status)
				otherChecked := e.row(t, other.ID).Checktime
				e.conn.set(dsconn.Info{Version: "8.0.40"}, nil)

				cr := &channelapi.ConfirmHostKeyResponse{}
				require.NoError(t, e.do(&channelapi.ConfirmHostKeyRequest{ID: jump.ID, Fingerprint: bastion.Fingerprint()}, cr))
				require.NotNil(t, cr.Item)
				assert.Equal(t, channel_entity.StatusOK, cr.Item.Status)
				for _, id := range []int64{direct.ID, deep.ID} {
					got := e.get(t, id)
					assert.Equal(t, datasource_entity.StatusOK, got.Status, got.Name)
					assert.Equal(t, "8.0.40", got.Server.Version, got.Name)
				}
				assert.Equal(t, "8.0.36", e.get(t, other.ID).Server.Version, "不经过该通道的数据源不重新测试")
				assert.Equal(t, otherChecked, e.row(t, other.ID).Checktime)
			})

			convey.Convey("在通道的编辑表单中信任新密钥并保存后，同样重新测试所有经过它的数据源", func() {
				require.NoError(t, e.do(&api.TestRequest{ID: deep.ID}, &api.TestResponse{}))
				assert.Equal(t, datasource_entity.StatusHostKeyChanged, e.get(t, deep.ID).Status)
				otherChecked := e.row(t, other.ID).Checktime
				e.conn.set(dsconn.Info{Version: "8.0.40"}, nil)

				host, port := hostPort(t, bastion.Addr())
				form := channelapi.Form{Name: "bastion-prod", Kind: "ssh", Host: host, Port: port, Username: sshUser,
					AuthMethod: "password", HostKey: bastion.Fingerprint()}
				ur := &channelapi.UpdateResponse{}
				require.NoError(t, e.do(&channelapi.UpdateRequest{ID: jump.ID, Channel: form}, ur))
				require.NotNil(t, ur.Item)
				assert.Equal(t, channel_entity.StatusOK, ur.Item.Status)
				for _, id := range []int64{direct.ID, deep.ID} {
					got := e.get(t, id)
					assert.Equal(t, datasource_entity.StatusOK, got.Status, got.Name)
					assert.Equal(t, "8.0.40", got.Server.Version, got.Name)
				}
				assert.Equal(t, otherChecked, e.row(t, other.ID).Checktime, "不经过该通道的数据源不重新测试")
			})
		})
	})
}

func TestChannelReferences(t *testing.T) {
	convey.Convey("通道的引用计数与删除保护计入数据源", t, func() {
		e := setupTest(t)
		proxy := newSOCKS5(t)
		socks := e.socksChannel(t, "office-socks", proxy)
		f := mysqlForm(t, "orders", newDB(t))
		f.ChannelID = socks.ID
		item := e.create(t, f)

		used := e.channel(t, socks.ID).UsedBy
		require.Len(t, used.DataSources, 1)
		assert.Equal(t, item.ID, used.DataSources[0].ID)
		assert.Equal(t, "orders", used.DataSources[0].Name)

		err := e.do(&channelapi.DeleteRequest{ID: socks.ID}, &channelapi.DeleteResponse{})
		assert.Equal(t, code.ChannelInUse, errCode(err))
		assert.Contains(t, err.Error(), "orders")

		require.NoError(t, e.do(&api.DeleteRequest{ID: item.ID}, &api.DeleteResponse{}))
		assert.Empty(t, e.list(t))
		assert.Empty(t, e.channel(t, socks.ID).UsedBy.DataSources)
		require.NoError(t, e.do(&channelapi.DeleteRequest{ID: socks.ID}, &channelapi.DeleteResponse{}))

		err = e.do(&api.DeleteRequest{ID: item.ID}, &api.DeleteResponse{})
		assert.Equal(t, code.DataSourceNotFound, errCode(err))
	})
}

func (e *env) findInList(t *testing.T, id int64) *api.Item {
	t.Helper()
	for _, it := range e.list(t) {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("数据源 %d 不在列表中", id)
	return nil
}

// probeGate 受控的假探测：调用后阻塞在 release 上，供测试精确控制“探测中”与“完成”之间的时序，
// 并记录调用次数（验证合并）与传入的 ctx 是否带有 60 秒整体超时。触发探测（如 Create）只保证同步标记
// 为“探测中”，不保证后台协程已经跑到 run；需要观察 run 内部状态（如 ctx 的超时）时先等 started
type probeGate struct {
	release chan struct{}
	started chan struct{}

	mu          sync.Mutex
	calls       int
	hasDeadline bool
	deadline    time.Time
}

func newProbeGate() *probeGate {
	return &probeGate{release: make(chan struct{}), started: make(chan struct{}, 8)}
}

func (g *probeGate) run(ctx context.Context, _ dsconn.Type, _ *dsconn.Conn) []probe.Item {
	g.mu.Lock()
	g.calls++
	g.deadline, g.hasDeadline = ctx.Deadline()
	g.mu.Unlock()
	g.started <- struct{}{}
	<-g.release
	return []probe.Item{
		{Key: "a", Title: probe.Text{ZhCN: "项 A", En: "Item A"}, Tier: probe.TierOK,
			Detail: probe.Text{ZhCN: "正常", En: "OK"}},
		{Key: "b", Title: probe.Text{ZhCN: "项 B", En: "Item B"}, Tier: probe.TierWarn,
			Detail: probe.Text{ZhCN: "有风险", En: "at risk"}, Fix: probe.Text{ZhCN: "修复", En: "fix"}},
	}
}

// waitProbe 等待一次后台探测完成（通过测试钩子），而不是靠 sleep 猜时间
func waitProbe(t *testing.T, done <-chan int64, id int64) {
	t.Helper()
	// 钩子是全局的：外层 Convey 先创建的数据源，其后台探测可能在钩子装上之后才结束，
	// 只认 id 的完成通知，跳过其它数据源的，否则会在 id 仍在探测中时提前返回
	timeout := time.After(2 * time.Second)
	for {
		select {
		case got := <-done:
			if got == id {
				return
			}
		case <-timeout:
			t.Fatalf("数据源 %d 的后台探测未在预期时间内完成", id)
		}
	}
}

// TestReprobe 覆盖 docs/specs/2026-09-25-datasources.md「能力探测」：保存成功后自动在后台探测一次，
// 完成前列表与详情都显示“探测中”；同一数据源同时只探测一次，重新探测时的第二次触发被合并；
// 结果带探测时间；连接失败或超时时显示“无法探测”且不显示上一次的结果；整体超时 60 秒
func TestReprobe(t *testing.T) {
	convey.Convey("能力探测", t, func() {
		e := setupTest(t)
		dbAddr := newDB(t)

		convey.Convey("保存成功后自动后台探测：完成前探测中，完成后有分档汇总、逐项结果与探测时间，超时 60 秒", func() {
			g := newProbeGate()
			datasource_svc.SetProbeRunner(g.run)
			done := make(chan int64, 4)
			datasource_svc.SetProbeDoneHook(func(id int64) { done <- id })
			t.Cleanup(func() { datasource_svc.SetProbeDoneHook(nil) })

			item := e.create(t, mysqlForm(t, "orders", dbAddr))
			require.NotNil(t, item.Probe)
			assert.Equal(t, "probing", item.Probe.State, "保存成功后列表这一行应显示探测中")
			assert.Equal(t, "probing", e.get(t, item.ID).Probe.State)
			assert.Equal(t, "probing", e.findInList(t, item.ID).Probe.State)

			select { // 等后台协程真正跑到 run（触发探测只同步保证标记为“探测中”），再检查它拿到的 ctx
			case <-g.started:
			case <-time.After(2 * time.Second):
				t.Fatal("后台探测未开始")
			}
			g.mu.Lock()
			hasDeadline, deadline := g.hasDeadline, g.deadline
			g.mu.Unlock()
			require.True(t, hasDeadline, "探测应受整体超时约束")
			assert.InDelta(t, 60, time.Until(deadline).Seconds(), 2, "单次探测的超时应为 60 秒")

			close(g.release)
			waitProbe(t, done, item.ID)

			got := e.get(t, item.ID)
			require.NotNil(t, got.Probe)
			assert.Equal(t, "done", got.Probe.State)
			assert.Equal(t, 1, got.Probe.OK)
			assert.Equal(t, 1, got.Probe.Warn)
			assert.Equal(t, 0, got.Probe.Fail)
			assert.NotZero(t, got.Probe.Time)
			require.Len(t, got.Probe.Items, 2)
			assert.Equal(t, "warn", got.Probe.Items[1].Tier)
			assert.Equal(t, "fix", got.Probe.Items[1].Fix.En)
			assert.Equal(t, "done", e.findInList(t, item.ID).Probe.State)
		})

		convey.Convey("重新探测：探测进行中再次触发会被合并，不会重复探测", func() {
			done := make(chan int64, 4)
			datasource_svc.SetProbeDoneHook(func(id int64) { done <- id })
			t.Cleanup(func() { datasource_svc.SetProbeDoneHook(nil) })
			item := e.create(t, mysqlForm(t, "orders", dbAddr))
			waitProbe(t, done, item.ID) // 先等创建时的自动探测完成，不与接下来手动触发的混在一起

			g := newProbeGate()
			datasource_svc.SetProbeRunner(g.run)

			resp1 := &api.ReprobeResponse{}
			require.NoError(t, e.do(&api.ReprobeRequest{ID: item.ID}, resp1))
			assert.Equal(t, "probing", resp1.Item.Probe.State)
			resp2 := &api.ReprobeResponse{}
			require.NoError(t, e.do(&api.ReprobeRequest{ID: item.ID}, resp2))
			assert.Equal(t, "probing", resp2.Item.Probe.State)

			close(g.release)
			waitProbe(t, done, item.ID)
			// 与 waitProbe 同理，只认本数据源的完成通知：外层先创建的数据源此时结束探测不算重复探测
			quiet := time.After(200 * time.Millisecond)
			for waiting := true; waiting; {
				select {
				case got := <-done:
					if got == item.ID {
						t.Fatal("同一数据源同时只应进行一次探测，第二次触发不应重复探测")
					}
				case <-quiet:
					waiting = false
				}
			}
			g.mu.Lock()
			calls := g.calls
			g.mu.Unlock()
			assert.Equal(t, 1, calls)
		})

		convey.Convey("探测进行中编辑并保存：结束后按新的设置再探测一次，不拿旧设置的结果冒充", func() {
			done := make(chan int64, 4)
			datasource_svc.SetProbeDoneHook(func(id int64) { done <- id })
			t.Cleanup(func() { datasource_svc.SetProbeDoneHook(nil) })
			g := newProbeGate()
			datasource_svc.SetProbeRunner(g.run)
			item := e.create(t, mysqlForm(t, "orders", dbAddr))
			select {
			case <-g.started:
			case <-time.After(2 * time.Second):
				t.Fatal("后台探测未开始")
			}
			f := mysqlForm(t, "orders", dbAddr)
			f.Username = "backup2"
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, DataSource: f}, &api.UpdateResponse{}))
			close(g.release)
			waitProbe(t, done, item.ID)
			g.mu.Lock()
			calls := g.calls
			g.mu.Unlock()
			assert.Equal(t, 2, calls, "保存后的新设置需要重新探测")
			assert.Equal(t, "backup2", e.conn.last().User)
		})

		convey.Convey("连接失败时显示无法探测且不显示上一次的结果", func() {
			done := make(chan int64, 4)
			datasource_svc.SetProbeDoneHook(func(id int64) { done <- id })
			t.Cleanup(func() { datasource_svc.SetProbeDoneHook(nil) })

			item := e.create(t, mysqlForm(t, "orders", dbAddr)) // 默认假探测：先有一次成功结果
			waitProbe(t, done, item.ID)
			require.Equal(t, "done", e.get(t, item.ID).Probe.State)

			e.conn.setOpenErr(errors.New("dial tcp 127.0.0.1:3306: connect: connection refused"))
			resp := &api.ReprobeResponse{}
			require.NoError(t, e.do(&api.ReprobeRequest{ID: item.ID}, resp))
			waitProbe(t, done, item.ID)

			got := e.get(t, item.ID)
			require.NotNil(t, got.Probe)
			assert.Equal(t, "unprobeable", got.Probe.State)
			assert.Contains(t, got.Probe.Error, "connection refused")
			assert.Empty(t, got.Probe.Items, "无法探测时不应显示上一次的结果")
		})
	})
}
