// Package server_svc 服务器：增删改查、凭据加密、指纹核对与测试连接
package server_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	serverapi "github.com/opskat/opsnap/internal/api/server"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
)

// credentialPending 新凭据还没落库时的占位 ID：校验阶段它只需要是一个合法的正整数，
// 落库前会被真实的凭据 ID 覆盖。取一个远大于自增范围的固定值，不会被当成真实 ID。
const credentialPending int64 = 1 << 62

// JobCounter 统计每台服务器被多少个任务使用，键是服务器 ID。
// 任务模块落地后通过 RegisterJobCounter 注册；未注册时任务数都是 0。
type JobCounter func(ctx context.Context) (map[int64]int, error)

var jobCounter JobCounter

// RegisterJobCounter 注册任务数统计；传 nil 表示没有任务
func RegisterJobCounter(f JobCounter) { jobCounter = f }

func countJobs(ctx context.Context) (map[int64]int, error) {
	if jobCounter == nil {
		return map[int64]int{}, nil
	}
	return jobCounter(ctx)
}

// ServerSvc 服务器服务
type ServerSvc interface {
	List(ctx context.Context) (*serverapi.ListResponse, error)
	Get(ctx context.Context, id int64) (*serverapi.GetResponse, error)
	// Create 添加服务器；目标主机指纹必须已核对
	Create(ctx context.Context, req *serverapi.CreateRequest) (*serverapi.CreateResponse, error)
	// Update 整体保存服务器配置；凭据为占位时保留已保存的值
	Update(ctx context.Context, req *serverapi.UpdateRequest) (*serverapi.UpdateResponse, error)
	// Delete 删除服务器；被任务使用或被其他服务器用作跳板时拒绝
	Delete(ctx context.Context, id int64) error
	// Test 测试未保存的配置，不改任何服务器的状态
	Test(ctx context.Context, req *serverapi.TestRequest) (*serverapi.TestResponse, error)
	// TestSaved 测试已保存的服务器，并记录连接状态
	TestSaved(ctx context.Context, id int64) (*serverapi.TestResponse, error)
}

type serverSvc struct {
	now func() time.Time
}

// NewServer 创建服务器服务；now 注入时钟
func NewServer(now func() time.Time) ServerSvc { return &serverSvc{now: now} }

var defaultServer = NewServer(time.Now)

// Server 取得服务器服务
func Server() ServerSvc { return defaultServer }

// RegisterServer 替换服务实现
func RegisterServer(i ServerSvc) { defaultServer = i }

func (s *serverSvc) find(ctx context.Context, id int64) (*server_entity.Server, error) {
	srv, err := server_repo.Server().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := srv.Check(ctx); err != nil {
		return nil, err
	}
	return srv, nil
}

func (s *serverSvc) List(ctx context.Context) (*serverapi.ListResponse, error) {
	list, err := server_repo.Server().List(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := countJobs(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(list))
	for _, srv := range list {
		names[srv.ID] = srv.Name
	}
	resp := &serverapi.ListResponse{List: make([]*serverapi.Item, 0, len(list))}
	for _, srv := range list {
		cred, err := s.credentialOf(ctx, srv)
		if err != nil {
			return nil, err
		}
		resp.List = append(resp.List, toItem(srv, cred, names, counts))
	}
	return resp, nil
}

func (s *serverSvc) Get(ctx context.Context, id int64) (*serverapi.GetResponse, error) {
	item, err := s.view(ctx, id)
	if err != nil {
		return nil, err
	}
	return &serverapi.GetResponse{Item: *item}, nil
}

// view 读取一台服务器并补齐跳板名称与任务数
func (s *serverSvc) view(ctx context.Context, id int64) (*serverapi.Item, error) {
	srv, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, h := range srv.Hops {
		if h.Type != server_entity.HopSSH {
			continue
		}
		jump, err := server_repo.Server().Find(ctx, h.ServerID)
		if err != nil {
			return nil, err
		}
		if jump != nil {
			names[jump.ID] = jump.Name
		}
	}
	counts, err := countJobs(ctx)
	if err != nil {
		return nil, err
	}
	cred, err := s.credentialOf(ctx, srv)
	if err != nil {
		return nil, err
	}
	return toItem(srv, cred, names, counts), nil
}

// credentialOf 读取服务器引用的登录凭据；引用不到时返回 nil（不能让一台坏数据拖垮整个列表）
func (s *serverSvc) credentialOf(ctx context.Context, srv *server_entity.Server) (*credential_entity.Credential, error) {
	if srv.CredentialID <= 0 {
		return nil, nil
	}
	return credential_repo.Credential().Find(ctx, srv.CredentialID)
}

func (s *serverSvc) Create(ctx context.Context, req *serverapi.CreateRequest) (*serverapi.CreateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	srv, cred, err := s.build(ctx, nil, req.Input())
	if err != nil {
		return nil, err
	}
	if err := srv.Validate(ctx); err != nil {
		return nil, err
	}
	// 配置已全部校验通过，先落凭据再写服务器
	if err := s.persistCredential(ctx, srv, cred); err != nil {
		return nil, err
	}
	now := s.now().Unix()
	srv.Createtime, srv.Updatetime = now, now
	if err := server_repo.Server().Create(ctx, srv); err != nil {
		return nil, err
	}
	item, err := s.view(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	return &serverapi.CreateResponse{Item: *item}, nil
}

func (s *serverSvc) Update(ctx context.Context, req *serverapi.UpdateRequest) (*serverapi.UpdateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	old, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	srv, cred, err := s.build(ctx, old, req.Input())
	if err != nil {
		return nil, err
	}
	if err := srv.Validate(ctx); err != nil {
		return nil, err
	}
	if err := s.persistCredential(ctx, srv, cred); err != nil {
		return nil, err
	}
	srv.Updatetime = s.now().Unix()
	if err := server_repo.Server().Update(ctx, srv); err != nil {
		return nil, err
	}
	// 连接目标变了：旧地址的连接状态和延迟不再适用，回到「未检查」
	if !old.SameEndpoint(srv) {
		if err := server_repo.Server().UpdateStatus(ctx, &server_entity.Server{ID: srv.ID}); err != nil {
			return nil, err
		}
	}
	item, err := s.view(ctx, srv.ID)
	if err != nil {
		return nil, err
	}
	return &serverapi.UpdateResponse{Item: *item}, nil
}

func (s *serverSvc) Delete(ctx context.Context, id int64) error {
	srv, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	list, err := server_repo.Server().List(ctx)
	if err != nil {
		return err
	}
	for _, other := range list {
		if other.ID != id && other.UsesJump(id) {
			return i18n.NewError(ctx, code.ServerUsedAsJump, other.Name)
		}
	}
	repos, err := repo_repo.Repo().List(ctx)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if r.Params.ServerID == id {
			return i18n.NewError(ctx, code.ServerUsedByRepo, r.Name)
		}
	}
	counts, err := countJobs(ctx)
	if err != nil {
		return err
	}
	if n := counts[id]; n > 0 {
		return i18n.NewError(ctx, code.ServerInUse, n)
	}
	if err := server_repo.Server().Delete(ctx, id); err != nil {
		return err
	}
	// 删除服务器时一并删掉它的私有凭据；共享凭据不动
	return s.deletePrivateCredential(ctx, srv)
}

func (s *serverSvc) Test(ctx context.Context, req *serverapi.TestRequest) (*serverapi.TestResponse, error) {
	var old *server_entity.Server
	if req.ID != 0 {
		var err error
		if old, err = s.find(ctx, req.ID); err != nil {
			return nil, err
		}
	}
	srv, cred, err := s.build(ctx, old, req.Input())
	if err != nil {
		return nil, err
	}
	if err := srv.ValidateConnection(ctx); err != nil {
		return nil, err
	}
	if srv.HostKeyFingerprint != "" && !server_entity.ValidFingerprint(srv.HostKeyFingerprint) {
		return nil, i18n.NewError(ctx, code.ServerFingerprintInvalid)
	}
	// 测试连接不落库，所以待写入的凭据只交给连接层，不写进 credentials
	if cred != nil {
		srv.CredentialID = credentialPending
	}
	cfg, err := sshconn.ServerConfigWithCredential(ctx, srv, cred)
	if err != nil {
		return nil, err
	}
	conn, results, err := sshconn.DialConfig(ctx, cfg)
	return testResponse(conn, results, err, len(srv.Hops))
}

func (s *serverSvc) TestSaved(ctx context.Context, id int64) (*serverapi.TestResponse, error) {
	srv, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	conn, results, err := sshconn.Dial(ctx, id, sshconn.WithNow(s.now))
	return testResponse(conn, results, err, len(srv.Hops))
}

// testResponse 把逐跳结果转换成接口响应并关闭连接。
// 某一跳失败属于正常的测试结果；其他错误（例如解密失败）原样返回。
func testResponse(conn *sshconn.Conn, results []sshconn.HopResult, err error, proxyHops int) (*serverapi.TestResponse, error) {
	if conn != nil {
		_ = conn.Close()
	}
	var he *sshconn.HopError
	if err != nil && !errors.As(err, &he) {
		return nil, err
	}
	resp := &serverapi.TestResponse{OK: err == nil, Hops: make([]*serverapi.HopResult, 0, len(results))}
	for _, r := range results {
		hr := &serverapi.HopResult{
			Index:       r.Index,
			Type:        string(r.Kind),
			Name:        r.Name,
			Addr:        r.Addr,
			OK:          r.OK,
			LatencyMs:   r.LatencyMs,
			Reason:      string(r.Reason),
			Message:     r.Message,
			Fingerprint: r.Fingerprint,
		}
		if !r.OK {
			hr.ReasonLabel = r.Reason.Label()
		}
		resp.Hops = append(resp.Hops, hr)
		if r.Index == proxyHops+1 {
			resp.Fingerprint = r.Fingerprint
		}
	}
	return resp, nil
}

// build 把提交的配置合并成实体：登录凭据合并成一条待写入的私有凭据（或沿用已保存的），
// 服务器只记 credential_id，并校验 SSH 跳板引用的服务器存在。old 为 nil 表示新建。
//
// 本阶段（数据层先落地）对外形状暂不变：auth_type 仍由请求传入。
// build 本身只读不写：返回待写入的凭据（nil 表示沿用现有的），由调用方在全部校验通过后落库，
// 这样「测试连接」不会凭空写出一条凭据，校验失败也不会留下孤立的行。
func (s *serverSvc) build(ctx context.Context, old *server_entity.Server, in *serverapi.ServerInput) (*server_entity.Server, *credential_entity.Credential, error) {
	if old == nil {
		old = &server_entity.Server{}
	}
	srv := &server_entity.Server{
		ID:                 old.ID,
		Name:               strings.TrimSpace(in.Name),
		Host:               strings.TrimSpace(in.Host),
		Port:               in.Port,
		Username:           strings.TrimSpace(in.Username),
		CredentialID:       old.CredentialID,
		HostKeyFingerprint: strings.TrimSpace(in.HostKeyFingerprint),
		Hops:               make([]server_entity.Hop, 0, len(in.Hops)),
		Createtime:         old.Createtime,
	}
	// 地址和端口不变时，留空表示沿用已核对的指纹
	if srv.HostKeyFingerprint == "" && old.ID != 0 && old.Host == srv.Host && old.Port == srv.Port {
		srv.HostKeyFingerprint = old.HostKeyFingerprint
	}

	cred, err := s.buildCredential(ctx, old, srv, in)
	if err != nil {
		return nil, nil, err
	}
	if cred != nil {
		if err := checkPrivateKey(ctx, cred); err != nil {
			return nil, nil, err
		}
	}

	for i, h := range in.Hops {
		hop, err := buildHop(ctx, i+1, srv.ID, h, old.Hops)
		if err != nil {
			return nil, nil, err
		}
		srv.Hops = append(srv.Hops, hop)
	}
	if srv.CredentialID == 0 {
		// 新凭据还没落库；用非 0 的占位通过「必须有凭据」，凭据内容已在 buildCredential 里校验过
		srv.CredentialID = credentialPending
	}
	return srv, cred, nil
}

// buildCredential 合并出这台服务器的登录凭据。旧凭据是私有的就原地更新（占位保留已保存的值），
// 旧凭据是共享的则新建一条私有凭据，不动原来那条共享的。返回 nil 表示这次没有要写的凭据。
func (s *serverSvc) buildCredential(ctx context.Context, old, srv *server_entity.Server, in *serverapi.ServerInput) (*credential_entity.Credential, error) {
	var saved *credential_entity.Credential
	if old.CredentialID > 0 {
		var err error
		if saved, err = credential_repo.Credential().Find(ctx, old.CredentialID); err != nil {
			return nil, err
		}
	}
	// 已经有一条自己的私有凭据：原地改写它
	var cred *credential_entity.Credential
	if saved != nil && !saved.IsShared() {
		cred = saved
	}
	if cred == nil {
		cred = &credential_entity.Credential{Shared: false}
	}

	enc := func(label, input, current string) (string, error) {
		// 密码会原样发给对方：地址、端口或用户改了就不能沿用已保存的，否则能借「测试连接」把它发到任意主机
		if input == secret.Masked && saved != nil &&
			(old.Host != srv.Host || old.Port != srv.Port || old.Username != srv.Username) {
			return "", i18n.NewError(ctx, code.ServerCredentialRetarget, label)
		}
		return credential(ctx, label, input, current)
	}

	var err error
	switch in.AuthType {
	case serverapi.AuthPassword:
		cred.Kind = credential_entity.KindPassword
		cred.PrivateKey, cred.Passphrase = "", ""
		if cred.Password, err = enc("密码", in.Password, cred.Password); err != nil {
			return nil, err
		}
	case serverapi.AuthPrivateKey:
		cred.Kind = credential_entity.KindSSHKey
		cred.Password = ""
		if cred.PrivateKey, err = enc("私钥", in.PrivateKey, cred.PrivateKey); err != nil {
			return nil, err
		}
		if cred.Passphrase, err = credential(ctx, "私钥口令", in.Passphrase, cred.Passphrase); err != nil {
			return nil, err
		}
	default:
		return nil, i18n.NewError(ctx, code.ServerAuthTypeInvalid)
	}
	// 对应类型的内容必须有（占位且没有已保存值时 credential 已经报错了，这里盖住新建时留空的情况）
	switch cred.Kind {
	case credential_entity.KindPassword:
		if cred.Password == "" {
			return nil, i18n.NewError(ctx, code.ServerCredentialRequired, "请填写密码")
		}
	case credential_entity.KindSSHKey:
		if cred.PrivateKey == "" {
			return nil, i18n.NewError(ctx, code.ServerCredentialRequired, "请填写私钥")
		}
	}
	return cred, nil
}

// persistCredential 把待写入的私有凭据落库，并把它的 ID 记到服务器上。
// cred 为 nil 表示沿用已有凭据，什么都不做。
func (s *serverSvc) persistCredential(ctx context.Context, srv *server_entity.Server, cred *credential_entity.Credential) error {
	if cred == nil {
		return nil
	}
	if err := s.saveCredential(ctx, cred); err != nil {
		return err
	}
	srv.CredentialID = cred.ID
	return nil
}

// saveCredential 在事务外写入或更新一条私有凭据
func (s *serverSvc) saveCredential(ctx context.Context, cred *credential_entity.Credential) error {
	now := s.now().Unix()
	cred.Updatetime = now
	if cred.ID == 0 {
		cred.Createtime = now
		return credential_repo.Credential().Create(ctx, cred)
	}
	return credential_repo.Credential().Update(ctx, cred)
}

// deletePrivateCredential 删除服务器自己的私有凭据；共享凭据不动。
// 这里只处理本阶段的最低行为：删除服务器时一并清理它的私有凭据。
func (s *serverSvc) deletePrivateCredential(ctx context.Context, srv *server_entity.Server) error {
	if srv.CredentialID <= 0 {
		return nil
	}
	cred, err := credential_repo.Credential().Find(ctx, srv.CredentialID)
	if err != nil {
		return err
	}
	if cred == nil || cred.IsShared() {
		return nil
	}
	return credential_repo.Credential().Delete(ctx, cred.ID)
}

// buildHop 合并一跳。SSH 跳板只保留 ServerID，并校验它是已添加的其他服务器；
// 代理的密码为占位时取同一代理已保存的密码。
func buildHop(ctx context.Context, index int, selfID int64, in serverapi.HopInput, oldHops []server_entity.Hop) (server_entity.Hop, error) {
	if in.Type == server_entity.HopSSH {
		hop := server_entity.Hop{Type: in.Type, ServerID: in.ServerID}
		if in.ServerID > 0 && in.ServerID != selfID {
			jump, err := server_repo.Server().Find(ctx, in.ServerID)
			if err != nil {
				return hop, err
			}
			if jump == nil {
				return hop, i18n.NewError(ctx, code.ServerHopInvalid, index, "作为跳板的服务器不存在")
			}
		}
		return hop, nil
	}
	hop := server_entity.Hop{
		Type:     in.Type,
		Host:     strings.TrimSpace(in.Host),
		Port:     in.Port,
		Username: strings.TrimSpace(in.Username),
	}
	var saved string
	for _, o := range oldHops {
		if o.SameProxy(hop) {
			saved = o.Password
			break
		}
	}
	var err error
	hop.Password, err = credential(ctx, fmt.Sprintf("代理链第 %d 跳的密码", index), in.Password, saved)
	return hop, err
}

// credential 处理一项凭据：空串表示没有，占位保留已保存的密文，其余加密保存
func credential(ctx context.Context, label, input, saved string) (string, error) {
	switch input {
	case "":
		return "", nil
	case secret.Masked:
		if saved == "" {
			return "", i18n.NewError(ctx, code.ServerCredentialMissing, label)
		}
		return saved, nil
	}
	sec := secret.Default()
	if sec == nil {
		return "", errors.New("主密钥未加载，无法加密服务器凭据")
	}
	return sec.Encrypt(input)
}

// checkPrivateKey 私钥认证时确认私钥能用口令解析
func checkPrivateKey(ctx context.Context, cred *credential_entity.Credential) error {
	if cred.Kind != credential_entity.KindSSHKey || cred.PrivateKey == "" {
		return nil
	}
	sec := secret.Default()
	if sec == nil {
		return errors.New("主密钥未加载，无法解密服务器凭据")
	}
	key, err := sec.Decrypt(cred.PrivateKey)
	if err != nil {
		return err
	}
	pass := ""
	if cred.Passphrase != "" {
		if pass, err = sec.Decrypt(cred.Passphrase); err != nil {
			return err
		}
	}
	if _, err := sshconn.ParsePrivateKey(key, pass); err != nil {
		return i18n.NewError(ctx, code.ServerPrivateKeyInvalid)
	}
	return nil
}

// toItem 转换成接口返回的结构。本阶段接口形状未变：认证方式由凭据类型推出，凭据只返回占位。
func toItem(srv *server_entity.Server, cred *credential_entity.Credential, names map[int64]string, counts map[int64]int) *serverapi.Item {
	item := &serverapi.Item{
		ID:                 srv.ID,
		Name:               srv.Name,
		Host:               srv.Host,
		Port:               srv.Port,
		Username:           srv.Username,
		Hops:               make([]*serverapi.HopView, 0, len(srv.Hops)),
		HostKeyFingerprint: srv.HostKeyFingerprint,
		Status:             srv.Status(),
		LastCheckAt:        srv.LastCheckAt,
		FailSince:          srv.FailSince,
		LastError:          srv.LastError,
		JobCount:           counts[srv.ID],
		CreatedAt:          srv.Createtime,
		UpdatedAt:          srv.Updatetime,
	}
	if item.Status == server_entity.StatusOK {
		item.LatencyMs = srv.LastLatencyMs
	}
	if cred != nil {
		switch cred.Kind {
		case credential_entity.KindPassword:
			item.AuthType, item.Password = serverapi.AuthPassword, secret.Mask(cred.Password)
		case credential_entity.KindSSHKey:
			item.AuthType = serverapi.AuthPrivateKey
			item.PrivateKey, item.Passphrase = secret.Mask(cred.PrivateKey), secret.Mask(cred.Passphrase)
		}
	}
	for _, h := range srv.Hops {
		item.Hops = append(item.Hops, &serverapi.HopView{
			Type:       h.Type,
			ServerID:   h.ServerID,
			ServerName: names[h.ServerID],
			Host:       h.Host,
			Port:       h.Port,
			Username:   h.Username,
			Password:   secret.Mask(h.Password),
		})
	}
	return item
}
