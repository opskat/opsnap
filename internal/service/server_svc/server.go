// Package server_svc 服务器：增删改查、凭据解析、指纹核对与测试连接。
//
// 登录凭据有两种来源：引用一条共享密钥（credential_id > 0），或者直接填写一份私有凭据
// （credential_id == 0）。私有凭据和共享密钥放在同一张 credentials 表里，用 shared 区分；
// 每台服务器最多有一条私有凭据，见 specs/2026-10-20-credential-library.md 决策 9 与 10。
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
	// Update 整体保存服务器配置；凭据为占位或留空时保留这台服务器已有的私有凭据
	Update(ctx context.Context, req *serverapi.UpdateRequest) (*serverapi.UpdateResponse, error)
	// Delete 删除服务器；被任务使用或被其他服务器用作跳板时拒绝。私有凭据一并删除，共享的不动
	Delete(ctx context.Context, id int64) error
	// Test 测试未保存的配置，不改任何服务器的状态，也不写任何凭据
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

// resolved 一次提交解析出来的登录凭据：
//   - ref 非 nil 表示引用了一条共享密钥，connect 用它，落库时不写凭据；
//   - private 非 nil 表示这次要写的私有凭据：可能是这台服务器已有的行（原地更新），
//     也可能是还没落库的新行（ID 为 0）。
//
// 两者至多一个非 nil。build 只读，写库由 persistCredential 单独完成，
// 所以「测试连接」不会凭空写出一条凭据，校验失败也不会留下孤立的行。
type resolved struct {
	ref     *credential_entity.Credential
	private *credential_entity.Credential
}

// credential 这次连接要用的凭据；没有任何凭据时返回 nil
func (r *resolved) credential() *credential_entity.Credential {
	if r == nil {
		return nil
	}
	if r.ref != nil {
		return r.ref
	}
	return r.private
}

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
	srv, res, err := s.build(ctx, nil, req.Input())
	if err != nil {
		return nil, err
	}
	if err := validateServer(ctx, srv, res, true); err != nil {
		return nil, err
	}
	// 配置已全部校验通过，先落凭据再写服务器
	if err := s.persistCredential(ctx, srv, res); err != nil {
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
	srv, res, err := s.build(ctx, old, req.Input())
	if err != nil {
		return nil, err
	}
	if err := validateServer(ctx, srv, res, true); err != nil {
		return nil, err
	}
	if err := s.persistCredential(ctx, srv, res); err != nil {
		return nil, err
	}
	// 从直接填写切到共享密钥：原来那条私有凭据不再被任何服务器引用，不能留成孤儿行
	srv.Updatetime = s.now().Unix()
	if err := server_repo.Server().Update(ctx, srv); err != nil {
		return nil, err
	}
	orphan, err := s.orphanPrivate(ctx, old, res)
	if err != nil {
		return nil, err
	}
	if orphan != nil {
		if err := credential_repo.Credential().Delete(ctx, orphan.ID); err != nil {
			return nil, err
		}
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
	srv, res, err := s.build(ctx, old, req.Input())
	if err != nil {
		return nil, err
	}
	if err := validateServer(ctx, srv, res, false); err != nil {
		return nil, err
	}
	if srv.HostKeyFingerprint != "" && !server_entity.ValidFingerprint(srv.HostKeyFingerprint) {
		return nil, i18n.NewError(ctx, code.ServerFingerprintInvalid)
	}
	// 测试连接不落库：解析出来的凭据只交给连接层，不写进 credentials
	cfg, err := sshconn.ServerConfigWithCredential(ctx, srv, res.credential())
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

// build 把提交的配置合并成实体：登录凭据解析成「引用一条共享密钥」或「一份待写入的私有凭据」，
// 服务器只记 credential_id，并校验 SSH 跳板引用的服务器存在。old 为 nil 表示新建。
// build 本身只读不写，返回的 resolved 由调用方在全部校验通过后落库。
func (s *serverSvc) build(ctx context.Context, old *server_entity.Server, in *serverapi.ServerInput) (*server_entity.Server, *resolved, error) {
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

	res, err := s.resolveCredential(ctx, old, srv, in)
	if err != nil {
		return nil, nil, err
	}
	if cred := res.credential(); cred != nil {
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
	return srv, res, nil
}

// validateServer 用服务器实体校验连接字段与指纹。res 是这次解析出来的凭据：
// 没有凭据时给出「请选择密码或私钥」；待写入的私有凭据还没落库（ID 为 0），
// 而实体只要求 credential_id > 0，所以校验时在内存副本上补一个占位 ID 表示「凭据已就位」。
// 这个占位只在栈上存在，不写库、也不会出现在持久化的实体里（落库前会写入真实的凭据 ID）。
func validateServer(ctx context.Context, srv *server_entity.Server, res *resolved, needFingerprint bool) error {
	if res == nil || res.credential() == nil {
		return i18n.NewError(ctx, code.ServerCredentialRequired, "请选择密码或私钥")
	}
	probe := *srv
	if probe.CredentialID <= 0 {
		probe.CredentialID = 1
	}
	if needFingerprint {
		return probe.Validate(ctx)
	}
	return probe.ValidateConnection(ctx)
}

// resolveCredential 解析这次提交的登录凭据。
//
// credential_id > 0：引用一条共享密钥，找不到和指向私有凭据都按「密钥不存在」处理；
// credential_id == 0：直接填写，这台服务器已有的私有凭据原地更新（占位或留空表示保留原值），
// 原来引用共享密钥时新建一条私有凭据，不动那条共享的。share_as 非空时把这份私有凭据原地转成共享。
func (s *serverSvc) resolveCredential(ctx context.Context, old, srv *server_entity.Server, in *serverapi.ServerInput) (*resolved, error) {
	if in.CredentialID > 0 {
		cred, err := credential_repo.Credential().Find(ctx, in.CredentialID)
		if err != nil {
			return nil, err
		}
		if cred == nil || !cred.IsShared() {
			return nil, i18n.NewError(ctx, code.CredentialNotFound)
		}
		srv.CredentialID = cred.ID
		return &resolved{ref: cred}, nil
	}

	saved, err := s.privateCredentialOf(ctx, old)
	if err != nil {
		return nil, err
	}
	// 已经有一条自己的私有凭据：原地改写它，同一台服务器不会出现第二条
	cred := saved
	if cred == nil {
		cred = &credential_entity.Credential{Shared: false}
	}
	if err := mergeCredential(ctx, old, srv, in, cred, saved); err != nil {
		return nil, err
	}
	srv.CredentialID = cred.ID
	return &resolved{private: cred}, nil
}

// privateCredentialOf 取这台服务器当前自己的私有凭据。引用的是一条共享密钥时返回 nil。
func (s *serverSvc) privateCredentialOf(ctx context.Context, old *server_entity.Server) (*credential_entity.Credential, error) {
	if old.CredentialID <= 0 {
		return nil, nil
	}
	cred, err := credential_repo.Credential().Find(ctx, old.CredentialID)
	if err != nil {
		return nil, err
	}
	if cred == nil || cred.IsShared() {
		return nil, nil
	}
	return cred, nil
}

// mergeCredential 把直接填写的凭据合并进 cred：按 auth_type 加密密码或私钥，
// 占位或留空（编辑已有私有凭据时）表示保留已保存的值；share_as 非空时原地转成这个名称的共享密钥（行 ID 不变）。
func mergeCredential(ctx context.Context, old, srv *server_entity.Server, in *serverapi.ServerInput, cred, saved *credential_entity.Credential) error {
	kind := in.AuthType
	if kind == "" && saved != nil {
		kind = saved.Kind
	}
	if kind == "" {
		return i18n.NewError(ctx, code.ServerCredentialRequired, "请选择密码或私钥")
	}
	// 类型从一端切到另一端时，另一侧的内容一并清掉
	switch kind {
	case credential_entity.KindPassword:
		cred.Kind = credential_entity.KindPassword
		cred.PrivateKey, cred.Passphrase = "", ""
	case credential_entity.KindSSHKey:
		cred.Kind = credential_entity.KindSSHKey
		cred.Password = ""
	default:
		return i18n.NewError(ctx, code.ServerAuthTypeInvalid)
	}
	// 地址、端口或用户改了就不能沿用已保存的密码：否则能借「测试连接」把它发到任意主机
	retarget := saved != nil && (old.Host != srv.Host || old.Port != srv.Port || old.Username != srv.Username)

	var err error
	switch cred.Kind {
	case credential_entity.KindPassword:
		if cred.Password, err = mergeField(ctx, "密码", in.Password, cred.Password, saved != nil, retarget); err != nil {
			return err
		}
		if cred.Password == "" {
			return i18n.NewError(ctx, code.ServerCredentialRequired, "请填写密码")
		}
	case credential_entity.KindSSHKey:
		if cred.PrivateKey, err = mergeField(ctx, "私钥", in.PrivateKey, cred.PrivateKey, saved != nil, retarget); err != nil {
			return err
		}
		if cred.PrivateKey == "" {
			return i18n.NewError(ctx, code.ServerCredentialRequired, "请填写私钥")
		}
		// 口令是可选内容：占位或留空都表示保留，没有原值就是没有
		if !isKeep(in.Passphrase) {
			if cred.Passphrase, err = encrypt(in.Passphrase); err != nil {
				return err
			}
		}
	}

	if name := strings.TrimSpace(in.ShareAs); name != "" {
		// 「同时存成密钥」的名称默认是服务器名，重名时自动加序号（web-01、web-01-2……），
		// 已经是共享的那条沿用自己原来的名称，不会因为重名又变成 web-01-2
		list, err := credential_repo.Credential().ListShared(ctx)
		if err != nil {
			return err
		}
		cred.Shared, cred.Name = true, credential_entity.FreeSharedName(name, cred.ID, list)
	}
	return cred.Validate(ctx)
}

// isKeep 空串与占位都表示「保留已保存的值」
func isKeep(input string) bool { return input == "" || input == secret.Masked }

// mergeField 合并一项必需的内容：
//   - 占位「已设置」保留已保存的密文，没有已保存的值时报「没有已保存的值，请填写」；
//   - 留空在编辑已有私有凭据时同样保留，新建时报空（由调用方给出「请填写密码 / 私钥」）；
//   - 其余加密保存。
//
// 连接目标变了时不沿用已保存的值（retarget），否则能借「测试连接」把已保存的密码发到任意主机。
func mergeField(ctx context.Context, label, input, current string, hasSaved, retarget bool) (string, error) {
	switch input {
	case secret.Masked:
		if current == "" {
			return "", i18n.NewError(ctx, code.ServerCredentialMissing, label)
		}
	case "":
		if !hasSaved {
			return "", nil
		}
	default:
		return encrypt(input)
	}
	if retarget {
		return "", i18n.NewError(ctx, code.ServerCredentialRetarget, label)
	}
	return current, nil
}

// encrypt 用主密钥加密一项内容；返回的错误不带内容，避免把明文写进错误文案或日志
func encrypt(input string) (string, error) {
	sec := secret.Default()
	if sec == nil {
		return "", errors.New("主密钥未加载，无法加密服务器凭据")
	}
	return sec.Encrypt(input)
}

// persistCredential 把待写入的私有凭据落库，并把它的 ID 记到服务器上。
// 引用共享密钥时什么都不写：srv.CredentialID 在解析阶段已经是那条密钥的 ID。
func (s *serverSvc) persistCredential(ctx context.Context, srv *server_entity.Server, res *resolved) error {
	if res == nil || res.private == nil {
		return nil
	}
	cred := res.private
	now := s.now().Unix()
	cred.Updatetime = now
	if cred.ID == 0 {
		cred.Createtime = now
		if err := credential_repo.Credential().Create(ctx, cred); err != nil {
			return duplicateError(ctx, err, cred.Name)
		}
	} else if err := credential_repo.Credential().Update(ctx, cred); err != nil {
		return duplicateError(ctx, err, cred.Name)
	}
	srv.CredentialID = cred.ID
	return nil
}

// duplicateError 把共享名称的唯一索引错误翻译成 CredentialNameDuplicate。
// 开号时已经避开重名，并发保存时仍可能同时拿到同一个空位，这里兜住；不把数据库错误暴露给调用方。
func duplicateError(ctx context.Context, err error, name string) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique constraint") {
		return i18n.NewError(ctx, code.CredentialNameDuplicate, name)
	}
	return err
}

// orphanPrivate 找出这次切到共享密钥后没人再用的私有凭据；没有就返回 nil。
// 服务器已经写完再调用它，避免写入失败时丢掉还在用的凭据。
func (s *serverSvc) orphanPrivate(ctx context.Context, old *server_entity.Server, res *resolved) (*credential_entity.Credential, error) {
	if res == nil || res.ref == nil {
		return nil, nil
	}
	private, err := s.privateCredentialOf(ctx, old)
	if err != nil {
		return nil, err
	}
	if private == nil {
		return nil, nil
	}
	n, err := credential_repo.Credential().CountServersByCredentialID(ctx, private.ID)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, nil
	}
	return private, nil
}

// deletePrivateCredential 删除服务器自己的私有凭据；共享凭据不动。
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
	hop.Password, err = proxyCredential(ctx, fmt.Sprintf("代理链第 %d 跳的密码", index), in.Password, saved)
	return hop, err
}

// proxyCredential 处理一跳代理的密码：留空表示没有密码，占位保留同一代理已保存的密码，其余加密保存
func proxyCredential(ctx context.Context, label, input, saved string) (string, error) {
	switch input {
	case "":
		return "", nil
	case secret.Masked:
		if saved == "" {
			return "", i18n.NewError(ctx, code.ServerCredentialMissing, label)
		}
		return saved, nil
	}
	return encrypt(input)
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

// toItem 转换成接口返回的结构。凭据只返回「有没有内容」与名称，不返回任何密文或明文。
func toItem(srv *server_entity.Server, cred *credential_entity.Credential, names map[int64]string, counts map[int64]int) *serverapi.Item {
	item := &serverapi.Item{
		ID:                 srv.ID,
		Name:               srv.Name,
		Host:               srv.Host,
		Port:               srv.Port,
		Username:           srv.Username,
		Credential:         credentialView(cred, srv.CredentialID),
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

// credentialView 凭据的摘要。私有凭据属于这台服务器，没有名称、默认用户和备注。
// 凭据行读不到（库里被改坏）时返回一个只带 ID 的空壳，接口形状保持稳定。
func credentialView(cred *credential_entity.Credential, fallbackID int64) *serverapi.CredentialView {
	if cred == nil {
		return &serverapi.CredentialView{ID: fallbackID}
	}
	view := &serverapi.CredentialView{
		ID:            cred.ID,
		Kind:          cred.Kind,
		Shared:        cred.IsShared(),
		HasPassphrase: cred.Kind == credential_entity.KindSSHKey && cred.Passphrase != "",
	}
	if view.Shared {
		view.Name, view.Username, view.Note = cred.Name, cred.Username, cred.Note
	}
	switch cred.Kind {
	case credential_entity.KindPassword:
		view.HasSecret = cred.Password != ""
	case credential_entity.KindSSHKey:
		view.HasSecret = cred.PrivateKey != ""
	}
	return view
}
