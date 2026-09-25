package channel_ctr

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	api "github.com/opskat/opsnap/internal/api/channel"
	tokenapi "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/channel_svc"
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
	keyPassphrase = "key-phrase-8Lm2" //nolint:gosec // 测试私钥的口令
)

type env struct {
	ctx   context.Context
	mux   *muxtest.TestMux
	token string // API 令牌
}

func setupChannelTest(t *testing.T) *env {
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	token_repo.RegisterToken(token_repo.NewToken())
	channel_repo.RegisterChannel(channel_repo.NewChannel())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	channel_svc.SetDataSourceReferrer(nil)
	channel_svc.SetHostKeyConfirmedHook(nil)
	channel_svc.SetHostKeyChangedHook(nil)

	setupCode, _ := auth_svc.Auth().PrepareSetupCode(ctx)
	_, _, err = auth_svc.Auth().Setup(ctx, &authapi.SetupRequest{SetupCode: setupCode, Username: "admin", Password: adminPassword},
		auth_svc.ClientMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	tok, err := token_svc.Token().Create(ctx, &tokenapi.CreateRequest{Name: "ci"})
	require.NoError(t, err)

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	ctr := NewChannel()
	authed := testMux.Group("/api/v1", middleware.SameOrigin()).Group("/", middleware.Auth())
	authed.Bind(ctr.List, ctr.Probe, ctr.Create, ctr.Update, ctr.Test, ctr.ConfirmHostKey, ctr.Delete)
	return &env{ctx: ctx, mux: testMux, token: tok.Token}
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

func newSSH(t *testing.T, keys ...ssh.PublicKey) *fakessh.Server {
	t.Helper()
	srv, err := fakessh.Listen("127.0.0.1:0", fakessh.Config{User: sshUser, Password: sshPassword, AuthorizedKeys: keys})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func newSOCKS5(t *testing.T, user, password string) *fakessh.SOCKS5 {
	t.Helper()
	p, err := fakessh.ListenSOCKS5("127.0.0.1:0", fakessh.SOCKS5Config{User: user, Password: password})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func sshForm(t *testing.T, name string, srv *fakessh.Server) api.Form {
	host, port := hostPort(t, srv.Addr())
	return api.Form{Name: name, Kind: "ssh", Host: host, Port: port, Username: sshUser, AuthMethod: "password", Password: sshPassword}
}

func socksForm(t *testing.T, name string, p *fakessh.SOCKS5, user, password string) api.Form {
	host, port := hostPort(t, p.Addr())
	return api.Form{Name: name, Kind: "socks5", Host: host, Port: port, Username: user, Password: password}
}

// create 新建通道；需要确认主机密钥时信任出示的指纹后重试
func (e *env) create(t *testing.T, f api.Form) *api.Item {
	t.Helper()
	resp := &api.CreateResponse{}
	require.NoError(t, e.do(&api.CreateRequest{Channel: f}, resp))
	if resp.HostKey != nil {
		f.HostKey = resp.HostKey.Fingerprint
		resp = &api.CreateResponse{}
		require.NoError(t, e.do(&api.CreateRequest{Channel: f}, resp))
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

func (e *env) item(t *testing.T, id int64) *api.Item {
	t.Helper()
	for _, it := range e.list(t) {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("通道 %d 不在列表中", id)
	return nil
}

func (e *env) row(t *testing.T, id int64) *channel_entity.Channel {
	t.Helper()
	var row channel_entity.Channel
	require.NoError(t, db.Ctx(e.ctx).First(&row, id).Error)
	return &row
}

func (e *env) decrypt(t *testing.T, ct string) string {
	t.Helper()
	pt, err := secret_svc.Secret().Decrypt(e.ctx, ct)
	require.NoError(t, err)
	return pt
}

func chainNames(hops []*api.Hop) []string {
	names := make([]string, 0, len(hops))
	for _, h := range hops {
		names = append(names, h.Name)
	}
	return names
}

func TestCreate(t *testing.T) {
	convey.Convey("新建通道", t, func() {
		e := setupChannelTest(t)
		srv := newSSH(t)

		convey.Convey("首次连接暂停在主机密钥确认，不认证、不保存；带上信任的指纹重试后保存", func() {
			f := sshForm(t, "bastion", srv)
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{Channel: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.Nil(t, resp.Item)
			assert.Equal(t, 1, resp.HostKey.Hop)
			assert.Equal(t, "bastion", resp.HostKey.Name)
			assert.Equal(t, srv.Addr(), resp.HostKey.Address)
			assert.Equal(t, srv.Fingerprint(), resp.HostKey.Fingerprint)
			assert.Equal(t, ssh.KeyAlgoED25519, resp.HostKey.KeyType)
			assert.False(t, resp.HostKey.Changed)
			assert.Zero(t, srv.AuthAttempts(), "主机密钥未确认时不发送任何凭据")
			assert.Empty(t, e.list(t))

			f.HostKey = resp.HostKey.Fingerprint
			resp = &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{Channel: f}, resp))
			require.NotNil(t, resp.Item)
			assert.Nil(t, resp.HostKey)
			assert.Equal(t, channel_entity.StatusOK, resp.Item.Status)
			assert.Equal(t, srv.Fingerprint(), resp.Item.HostKey)
			assert.True(t, resp.Item.HasPassword)
			assert.Equal(t, "password", resp.Item.AuthMethod)
			assert.Equal(t, "ssh://"+sshUser+"@"+srv.Addr(), resp.Item.Address)
			assert.NotZero(t, resp.Item.CheckedAt)
			assert.Equal(t, []string{"bastion"}, chainNames(resp.Item.Chain))

			row := e.row(t, resp.Item.ID)
			assert.NotEmpty(t, row.Password)
			assert.NotContains(t, row.Password, sshPassword, "数据库中的密码是密文")
			assert.Equal(t, sshPassword, e.decrypt(t, row.Password))

			var raw map[string]any
			require.NoError(t, e.do(&api.ListRequest{}, &raw))
			body, _ := json.Marshal(raw)
			assert.NotContains(t, string(body), sshPassword, "接口不返回秘密")
			assert.Contains(t, string(body), "bastion")
		})

		convey.Convey("主机出示的密钥与信任的指纹不一致时不认证、不保存", func() {
			f := sshForm(t, "bastion", srv)
			f.HostKey = "SHA256:not-this-one"
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{Channel: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.True(t, resp.HostKey.Changed)
			assert.Equal(t, "SHA256:not-this-one", resp.HostKey.Saved)
			assert.Equal(t, srv.Fingerprint(), resp.HostKey.Fingerprint)
			assert.Zero(t, srv.AuthAttempts())
			assert.Empty(t, e.list(t))
		})

		convey.Convey("认证失败时指出第几跳并且不保存", func() {
			f := sshForm(t, "bastion", srv)
			f.HostKey = srv.Fingerprint()
			f.Password = "wrong"
			err := e.do(&api.CreateRequest{Channel: f}, &api.CreateResponse{})
			assert.Equal(t, code.ChannelHopFailed, errCode(err))
			assert.Contains(t, err.Error(), "第 1 跳 bastion（SSH）：认证失败")
			assert.Empty(t, e.list(t))
		})

		convey.Convey("字段校验", func() {
			host, port := hostPort(t, srv.Addr())
			ssh1 := func(mod func(*api.Form)) api.Form {
				f := api.Form{Name: "a", Kind: "ssh", Host: host, Port: port, Username: sshUser, AuthMethod: "password", Password: "x"}
				mod(&f)
				return f
			}
			cases := []struct {
				form api.Form
				want int
			}{
				{ssh1(func(f *api.Form) { f.Name = " " }), code.ChannelNameInvalid},
				{ssh1(func(f *api.Form) { f.Name = strings.Repeat("名", 65) }), code.ChannelNameInvalid},
				{ssh1(func(f *api.Form) { f.Host = " " }), code.ChannelHostRequired},
				{ssh1(func(f *api.Form) { f.Port = 65536 }), code.ChannelPortInvalid},
				{ssh1(func(f *api.Form) { f.Port = -1 }), code.ChannelPortInvalid},
				{ssh1(func(f *api.Form) { f.Username = "" }), code.ChannelUserRequired},
				{ssh1(func(f *api.Form) { f.AuthMethod = "" }), code.ChannelAuthMethodInvalid},
				{ssh1(func(f *api.Form) { f.Password = "" }), code.ChannelPasswordRequired},
				{ssh1(func(f *api.Form) { f.AuthMethod = "key" }), code.ChannelPrivateKeyRequired},
				{ssh1(func(f *api.Form) { f.AuthMethod = "key"; f.PrivateKey = "not a key" }), code.ChannelKeyInvalid},
				{api.Form{Name: "s", Kind: "socks5", Host: host, Port: port, Username: "u"}, code.ChannelSOCKS5CredentialPair},
				{api.Form{Name: "s", Kind: "socks5", Host: host, Port: port, Password: "p"}, code.ChannelSOCKS5CredentialPair},
				{ssh1(func(f *api.Form) { f.ViaID = 999 }), code.ChannelViaNotFound},
			}
			for _, c := range cases {
				assert.Equal(t, c.want, errCode(e.do(&api.CreateRequest{Channel: c.form}, &api.CreateResponse{})), "%+v", c.form)
				assert.Equal(t, c.want, errCode(e.do(&api.ProbeRequest{Channel: c.form}, &api.ProbeResponse{})), "%+v", c.form)
			}
			assert.Zero(t, srv.AuthAttempts())
			assert.Empty(t, e.list(t))
		})

		convey.Convey("名称不能与已有通道重复", func() {
			e.create(t, sshForm(t, "bastion", srv))
			err := e.do(&api.CreateRequest{Channel: sshForm(t, "bastion", srv)}, &api.CreateResponse{})
			assert.Equal(t, code.ChannelNameDuplicate, errCode(err))
			assert.Len(t, e.list(t), 1)
		})

		convey.Convey("私钥认证：已加密时缺少或填错口令分别提示，正确时保存", func() {
			key, pub := testKey(t, keyPassphrase)
			keySrv := newSSH(t, pub)
			f := sshForm(t, "by-key", keySrv)
			f.AuthMethod, f.Password, f.PrivateKey = "key", "", key

			err := e.do(&api.CreateRequest{Channel: f}, &api.CreateResponse{})
			assert.Equal(t, code.ChannelPassphraseMissing, errCode(err))
			f.Passphrase = "wrong"
			err = e.do(&api.CreateRequest{Channel: f}, &api.CreateResponse{})
			assert.Equal(t, code.ChannelPassphraseWrong, errCode(err))
			assert.Zero(t, keySrv.AuthAttempts())

			f.Passphrase = keyPassphrase
			item := e.create(t, f)
			assert.Equal(t, "key", item.AuthMethod)
			assert.True(t, item.HasPrivateKey)
			assert.True(t, item.HasPassphrase)
			assert.False(t, item.HasPassword)
			row := e.row(t, item.ID)
			assert.NotContains(t, row.PrivateKey, "OPENSSH PRIVATE KEY")
			assert.Equal(t, key, e.decrypt(t, row.PrivateKey))
			assert.Equal(t, keyPassphrase, e.decrypt(t, row.Passphrase))
		})

		convey.Convey("SOCKS5 → SSH：经由代理连接跳板，链路按顺序显示", func() {
			proxy := newSOCKS5(t, socksUser, socksPassword)
			socks := e.create(t, socksForm(t, "office-socks", proxy, socksUser, socksPassword))
			assert.Equal(t, "password", socks.AuthMethod)
			assert.Equal(t, "socks5://"+proxy.Addr(), socks.Address)

			f := sshForm(t, "bastion", srv)
			f.ViaID = socks.ID
			resp := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{Channel: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.Equal(t, 2, resp.HostKey.Hop)
			item := e.create(t, f)
			assert.Equal(t, []string{"office-socks", "bastion"}, chainNames(item.Chain))
			assert.Equal(t, socks.ID, item.ViaID)
			assert.Contains(t, proxy.Requests(), srv.Addr())
		})

		convey.Convey("SOCKS5 认证失败时指出是第几跳哪个通道", func() {
			proxy := newSOCKS5(t, socksUser, socksPassword)
			err := e.do(&api.ProbeRequest{Channel: socksForm(t, "office-socks", proxy, socksUser, "wrong")}, &api.ProbeResponse{})
			assert.Equal(t, code.ChannelHopFailed, errCode(err))
			assert.Contains(t, err.Error(), "第 1 跳 office-socks（SOCKS5）：认证失败")

			socks := e.create(t, socksForm(t, "office-socks", proxy, socksUser, socksPassword))
			require.NoError(t, proxy.Close())
			f := sshForm(t, "bastion", srv)
			f.ViaID = socks.ID
			err = e.do(&api.ProbeRequest{Channel: f}, &api.ProbeResponse{})
			assert.Equal(t, code.ChannelHopFailed, errCode(err))
			assert.Contains(t, err.Error(), "第 1 跳 office-socks（SOCKS5）：无法连接")
		})

		convey.Convey("测试连接只测试不保存，并返回完整链路", func() {
			f := sshForm(t, "bastion", srv)
			f.HostKey = srv.Fingerprint()
			resp := &api.ProbeResponse{}
			require.NoError(t, e.do(&api.ProbeRequest{Channel: f}, resp))
			assert.Nil(t, resp.HostKey)
			assert.Equal(t, []string{"bastion"}, chainNames(resp.Chain))
			assert.Empty(t, e.list(t))
		})
	})
}

func TestUpdate(t *testing.T) {
	convey.Convey("编辑通道", t, func() {
		e := setupChannelTest(t)
		key, pub := testKey(t, "")
		srv := newSSH(t, pub)
		f := sshForm(t, "bastion", srv)
		item := e.create(t, f)
		before := e.row(t, item.ID)

		convey.Convey("秘密留空表示沿用；主机未变时不需要重新确认主机密钥", func() {
			f.Name, f.Password, f.HostKey = "bastion-2", "", ""
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, resp))
			require.NotNil(t, resp.Item)
			assert.Equal(t, "bastion-2", resp.Item.Name)
			assert.Equal(t, sshPassword, e.decrypt(t, e.row(t, item.ID).Password))
		})

		convey.Convey("测试失败时保持编辑前的全部设置", func() {
			f.Name, f.Password = "renamed", "wrong"
			err := e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, &api.UpdateResponse{})
			assert.Equal(t, code.ChannelHopFailed, errCode(err))
			assert.Equal(t, before, e.row(t, item.ID))
		})

		convey.Convey("切换为私钥认证后，原来的密码被删除", func() {
			f.AuthMethod, f.Password, f.PrivateKey = "key", "", key
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, resp))
			assert.Equal(t, "key", resp.Item.AuthMethod)
			assert.False(t, resp.Item.HasPassword)
			assert.False(t, resp.Item.HasPassphrase)
			row := e.row(t, item.ID)
			assert.Empty(t, row.Password)
			assert.Equal(t, key, e.decrypt(t, row.PrivateKey))

			convey.Convey("再切回密码认证，私钥被删除", func() {
				f.AuthMethod, f.Password, f.PrivateKey = "password", sshPassword, ""
				require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, &api.UpdateResponse{}))
				row := e.row(t, item.ID)
				assert.Empty(t, row.PrivateKey)
				assert.Equal(t, sshPassword, e.decrypt(t, row.Password))
			})
		})

		convey.Convey("主机密钥已变化时返回待确认结果，不认证、不保存", func() {
			require.NoError(t, srv.RotateHostKey())
			attempts := srv.AuthAttempts()
			f.Name = "renamed"
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.True(t, resp.HostKey.Changed)
			assert.Equal(t, item.HostKey, resp.HostKey.Saved)
			assert.Equal(t, srv.Fingerprint(), resp.HostKey.Fingerprint)
			assert.Equal(t, attempts, srv.AuthAttempts())
			assert.Equal(t, before, e.row(t, item.ID))

			f.HostKey = resp.HostKey.Fingerprint
			resp = &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, resp))
			assert.Equal(t, srv.Fingerprint(), resp.Item.HostKey)
		})

		convey.Convey("换了主机时按首次连接确认主机密钥", func() {
			other := newSSH(t)
			f.Host, f.Port = hostPort(t, other.Addr())
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: item.ID, Channel: f}, resp))
			require.NotNil(t, resp.HostKey)
			assert.False(t, resp.HostKey.Changed)
			assert.Equal(t, other.Fingerprint(), resp.HostKey.Fingerprint)
		})

		convey.Convey("通道不存在", func() {
			err := e.do(&api.UpdateRequest{ID: 999, Channel: f}, &api.UpdateResponse{})
			assert.Equal(t, code.ChannelNotFound, errCode(err))
		})
	})
}

func TestChain(t *testing.T) {
	convey.Convey("经由：环路与链路长度", t, func() {
		e := setupChannelTest(t)
		proxy := newSOCKS5(t, "", "")

		// 同一个代理可以经由自己连接自己，用它串出多跳链路
		var ids []int64
		var via int64
		for i := 1; i <= netchain.MaxHops; i++ {
			f := socksForm(t, "p"+strconv.Itoa(i), proxy, "", "")
			f.ViaID = via
			it := e.create(t, f)
			assert.Len(t, it.Chain, i)
			ids = append(ids, it.ID)
			via = it.ID
		}

		convey.Convey("超过 5 跳不能保存，并提示当前链路长度", func() {
			f := socksForm(t, "p6", proxy, "", "")
			f.ViaID = via
			err := e.do(&api.CreateRequest{Channel: f}, &api.CreateResponse{})
			assert.Equal(t, code.ChannelChainTooLong, errCode(err))
			assert.Contains(t, err.Error(), "6")
			err = e.do(&api.ProbeRequest{Channel: f}, &api.ProbeResponse{})
			assert.Equal(t, code.ChannelChainTooLong, errCode(err))
		})

		convey.Convey("让链路起点再经由一个通道，会使经过它的通道超过 5 跳", func() {
			extra := e.create(t, socksForm(t, "extra", proxy, "", ""))
			f := socksForm(t, "p1", proxy, "", "")
			f.ViaID = extra.ID
			err := e.do(&api.UpdateRequest{ID: ids[0], Channel: f}, &api.UpdateResponse{})
			assert.Equal(t, code.ChannelChainTooLong, errCode(err))
		})

		convey.Convey("不能经由自己或会形成环路的通道", func() {
			f := socksForm(t, "p1", proxy, "", "")
			f.ViaID = ids[0]
			err := e.do(&api.UpdateRequest{ID: ids[0], Channel: f}, &api.UpdateResponse{})
			assert.Equal(t, code.ChannelViaCycle, errCode(err))
			f.ViaID = ids[2]
			err = e.do(&api.UpdateRequest{ID: ids[0], Channel: f}, &api.UpdateResponse{})
			assert.Equal(t, code.ChannelViaCycle, errCode(err))
			assert.Zero(t, e.row(t, ids[0]).ViaID)
		})
	})
}

func TestDelete(t *testing.T) {
	convey.Convey("删除通道", t, func() {
		e := setupChannelTest(t)
		proxy := newSOCKS5(t, "", "")
		a := e.create(t, socksForm(t, "a", proxy, "", ""))
		fb := socksForm(t, "b", proxy, "", "")
		fb.ViaID = a.ID
		b := e.create(t, fb)

		convey.Convey("仍被其他通道引用时拒绝，并列出引用它的对象", func() {
			item := e.item(t, a.ID)
			require.NotNil(t, item.UsedBy)
			require.Len(t, item.UsedBy.Channels, 1)
			assert.Equal(t, "b", item.UsedBy.Channels[0].Name)
			assert.Empty(t, item.UsedBy.DataSources)

			err := e.do(&api.DeleteRequest{ID: a.ID}, &api.DeleteResponse{})
			assert.Equal(t, code.ChannelInUse, errCode(err))
			assert.Contains(t, err.Error(), "b")
			assert.Len(t, e.list(t), 2)

			require.NoError(t, e.do(&api.DeleteRequest{ID: b.ID}, &api.DeleteResponse{}))
			require.NoError(t, e.do(&api.DeleteRequest{ID: a.ID}, &api.DeleteResponse{}))
			assert.Empty(t, e.list(t))
		})

		convey.Convey("仍被数据源引用时拒绝", func() {
			channel_svc.SetDataSourceReferrer(func(context.Context) (map[int64][]*api.Ref, error) {
				return map[int64][]*api.Ref{b.ID: {{ID: 7, Name: "mysql-prod"}}}, nil
			})
			item := e.item(t, b.ID)
			require.Len(t, item.UsedBy.DataSources, 1)
			assert.Equal(t, "mysql-prod", item.UsedBy.DataSources[0].Name)
			err := e.do(&api.DeleteRequest{ID: b.ID}, &api.DeleteResponse{})
			assert.Equal(t, code.ChannelInUse, errCode(err))
			assert.Contains(t, err.Error(), "mysql-prod")
		})

		convey.Convey("通道不存在", func() {
			err := e.do(&api.DeleteRequest{ID: 999}, &api.DeleteResponse{})
			assert.Equal(t, code.ChannelNotFound, errCode(err))
		})
	})
}

func TestHostKeyChanged(t *testing.T) {
	convey.Convey("主机密钥变化与重新确认", t, func() {
		e := setupChannelTest(t)
		srv := newSSH(t)
		jump := e.create(t, sshForm(t, "bastion", srv))
		proxy := newSOCKS5(t, "", "")
		fs := socksForm(t, "inner-socks", proxy, "", "")
		fs.ViaID = jump.ID
		inner := e.create(t, fs)
		oldKey := srv.Fingerprint()
		require.NoError(t, srv.RotateHostKey())
		attempts, forwards := srv.AuthAttempts(), len(srv.Forwards())
		var changed []int64
		channel_svc.SetHostKeyChangedHook(func(_ context.Context, id int64) error {
			changed = append(changed, id)
			return nil
		})

		convey.Convey("测试已保存的通道：中止连接，不认证，状态变为主机密钥已变化", func() {
			resp := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: jump.ID}, resp))
			assert.Equal(t, channel_entity.StatusHostKeyChanged, resp.Item.Status)
			assert.Equal(t, oldKey, resp.Item.HostKey)
			assert.Equal(t, srv.Fingerprint(), resp.Item.PresentedHostKey)
			assert.Contains(t, resp.Item.StatusMessage, "第 1 跳 bastion（SSH）：主机密钥已变化")
			require.NotNil(t, resp.HostKey)
			assert.True(t, resp.HostKey.Changed)
			assert.Equal(t, oldKey, resp.HostKey.Saved)
			assert.Equal(t, attempts, srv.AuthAttempts())
			assert.Equal(t, channel_entity.StatusHostKeyChanged, e.item(t, jump.ID).Status)

			var retested []int64
			channel_svc.SetHostKeyConfirmedHook(func(_ context.Context, id int64) error {
				retested = append(retested, id)
				return nil
			})
			assert.Equal(t, []int64{jump.ID}, changed, "经过它的数据源同样标为主机密钥已变化")

			convey.Convey("重新确认：指纹与出示的不一致时不保存", func() {
				cr := &api.ConfirmHostKeyResponse{}
				require.NoError(t, e.do(&api.ConfirmHostKeyRequest{ID: jump.ID, Fingerprint: oldKey}, cr))
				assert.Empty(t, retested, "没有保存新密钥时不重新测试数据源")
				assert.Nil(t, cr.Item)
				require.NotNil(t, cr.HostKey)
				assert.Equal(t, srv.Fingerprint(), cr.HostKey.Fingerprint)
				assert.Equal(t, oldKey, e.row(t, jump.ID).HostKey)
				assert.Equal(t, attempts, srv.AuthAttempts())
			})

			convey.Convey("重新确认：信任新密钥后保存并重新测试", func() {
				cr := &api.ConfirmHostKeyResponse{}
				require.NoError(t, e.do(&api.ConfirmHostKeyRequest{ID: jump.ID, Fingerprint: srv.Fingerprint()}, cr))
				require.NotNil(t, cr.Item)
				assert.Nil(t, cr.HostKey)
				assert.Equal(t, srv.Fingerprint(), cr.Item.HostKey)
				assert.Equal(t, channel_entity.StatusOK, cr.Item.Status)
				assert.Empty(t, cr.Item.PresentedHostKey)
				assert.Greater(t, srv.AuthAttempts(), attempts)
				assert.Equal(t, []int64{jump.ID}, retested, "保存新密钥后重新测试经过它的数据源")
			})
		})

		convey.Convey("经过它的通道测试时同样中止，两者都标为主机密钥已变化并指明是哪个通道", func() {
			resp := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: inner.ID}, resp))
			assert.Equal(t, channel_entity.StatusHostKeyChanged, resp.Item.Status)
			assert.Contains(t, resp.Item.StatusMessage, "第 1 跳 bastion（SSH）")
			assert.Empty(t, resp.Item.PresentedHostKey)
			assert.Nil(t, resp.HostKey)
			assert.Equal(t, attempts, srv.AuthAttempts())
			assert.Len(t, srv.Forwards(), forwards, "经由的主机密钥变化时不经它转发")
			assert.Equal(t, []int64{jump.ID}, changed, "经由的通道被标记时，经过它的数据源同样被标记")
			j := e.item(t, jump.ID)
			assert.Equal(t, channel_entity.StatusHostKeyChanged, j.Status)
			assert.Equal(t, srv.Fingerprint(), j.PresentedHostKey)

			err := e.do(&api.ProbeRequest{ID: inner.ID, Channel: fs}, &api.ProbeResponse{})
			assert.Equal(t, code.ChannelHostKeyChanged, errCode(err))
		})

		convey.Convey("SOCKS5 通道没有主机密钥", func() {
			err := e.do(&api.ConfirmHostKeyRequest{ID: inner.ID, Fingerprint: "SHA256:x"}, &api.ConfirmHostKeyResponse{})
			assert.Equal(t, code.ChannelNotSSH, errCode(err))
		})
	})
}

func TestTest(t *testing.T) {
	convey.Convey("测试已保存的通道", t, func() {
		e := setupChannelTest(t)
		proxy := newSOCKS5(t, socksUser, socksPassword)
		item := e.create(t, socksForm(t, "office-socks", proxy, socksUser, socksPassword))

		convey.Convey("无法连接时记录原因与时间，恢复后变回正常", func() {
			require.NoError(t, proxy.Close())
			resp := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: item.ID}, resp))
			assert.Equal(t, channel_entity.StatusUnreachable, resp.Item.Status)
			assert.Contains(t, resp.Item.StatusMessage, "第 1 跳 office-socks（SOCKS5）：无法连接")
			assert.NotZero(t, resp.Item.CheckedAt)
			assert.Equal(t, channel_entity.StatusUnreachable, e.item(t, item.ID).Status)
		})

		convey.Convey("通道不存在", func() {
			err := e.do(&api.TestRequest{ID: 999}, &api.TestResponse{})
			assert.Equal(t, code.ChannelNotFound, errCode(err))
		})
	})
}

func TestHops(t *testing.T) {
	convey.Convey("把通道解析为有序链路，供数据源建链", t, func() {
		e := setupChannelTest(t)
		proxy := newSOCKS5(t, socksUser, socksPassword)
		srv := newSSH(t)
		socks := e.create(t, socksForm(t, "office-socks", proxy, socksUser, socksPassword))
		f := sshForm(t, "bastion", srv)
		f.ViaID = socks.ID
		jump := e.create(t, f)

		hops, err := channel_svc.Channel().Hops(e.ctx, jump.ID)
		require.NoError(t, err)
		require.Len(t, hops, 2)
		assert.Equal(t, socks.ID, hops[0].ID)
		assert.Equal(t, netchain.KindSOCKS5, hops[0].Kind)
		assert.Equal(t, socksPassword, hops[0].Password)
		assert.Equal(t, jump.ID, hops[1].ID)
		assert.Equal(t, sshPassword, hops[1].Password)
		assert.Equal(t, srv.Fingerprint(), hops[1].HostKey)

		chain, err := netchain.NewChain(hops)
		require.NoError(t, err)
		tun, err := chain.Connect(e.ctx)
		require.NoError(t, err)
		require.NoError(t, tun.Close())

		refs, err := channel_svc.Channel().References(e.ctx, socks.ID)
		require.NoError(t, err)
		require.Len(t, refs.Channels, 1)
		assert.Equal(t, jump.ID, refs.Channels[0].ID)

		_, err = channel_svc.Channel().Hops(e.ctx, 999)
		assert.Equal(t, code.ChannelNotFound, errCode(err))
	})
}
