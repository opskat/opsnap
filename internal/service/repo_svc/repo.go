// Package repo_svc 存储位置：保存前测试访问、初始化或接入 restic 仓库、两把密钥、
// 本地目录的挂载检查、统计与维护（校验、清理、解锁）。
package repo_svc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"golang.org/x/crypto/ssh"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
)

// JobCounter 统计每个存储位置被多少个任务使用，键是存储位置 ID。
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

// RepoSvc 存储位置服务
type RepoSvc interface {
	List(ctx context.Context) (*repoapi.ListResponse, error)
	Get(ctx context.Context, id int64) (*repoapi.GetResponse, error)
	// Create 先测试访问：空位置初始化新仓库，已有仓库凭原密码接入；生成 opsnap 密钥和可选的恢复密钥
	Create(ctx context.Context, req *repoapi.CreateRequest) (*repoapi.CreateResponse, error)
	// Update 修改名称和地址参数，保存前用 opsnap 密钥验证仍能打开仓库
	Update(ctx context.Context, req *repoapi.UpdateRequest) (*repoapi.UpdateResponse, error)
	// Delete 删除记录，NAS 上的数据不动；被任务使用或正在使用时拒绝
	Delete(ctx context.Context, id int64) error
	// SFTPHostKey 取回 SFTP 主机出示的公钥与指纹
	SFTPHostKey(ctx context.Context, req *repoapi.SFTPHostKeyRequest) (*repoapi.SFTPHostKeyResponse, error)

	Keys(ctx context.Context, id int64) (*repoapi.KeysResponse, error)
	// RotateKey 更换 opsnap 密钥：加新密钥 → 验证 → 删旧密钥，任何一步失败都保持原状
	RotateKey(ctx context.Context, id int64) (*repoapi.RotateKeyResponse, error)
	// NewRecoveryKey 生成一把恢复密钥的值供保存前展示；不保存、不加入任何仓库
	NewRecoveryKey(ctx context.Context) (*repoapi.NewRecoveryKeyResponse, error)
	// GenerateRecoveryKey 把管理员已看到并保存的恢复密钥加入仓库；不保存它
	GenerateRecoveryKey(ctx context.Context, req *repoapi.GenerateRecoveryKeyRequest) (*repoapi.GenerateRecoveryKeyResponse, error)
	RemoveRecoveryKey(ctx context.Context, id int64) (*repoapi.RemoveRecoveryKeyResponse, error)
	// RecoveryBundle 恢复包：仓库地址、恢复步骤与 restic 命令，不含任何密钥或密码
	RecoveryBundle(ctx context.Context, id int64) (*repoapi.RecoveryBundleResponse, error)

	// Recheck 重新检查挂载并刷新统计
	Recheck(ctx context.Context, id int64) (*repoapi.RecheckResponse, error)
	// StartCheck 在后台独占执行校验数据
	StartCheck(ctx context.Context, id int64) (*repoapi.CheckResponse, error)
	// StartPrune 在后台独占执行清理空间
	StartPrune(ctx context.Context, id int64) (*repoapi.PruneResponse, error)
	// Unlock 解除仓库锁，confirm 必须为 true
	Unlock(ctx context.Context, id int64, confirm bool) error

	// Preflight 运行前检查：本地目录缺少 .opsnap-repo 时返回包裹 ErrNotMounted 的错误，不写入任何数据
	Preflight(ctx context.Context, id int64) error
	// Env 返回访问仓库所需的 cmdbuild.Repo（含解密后的 opsnap 密钥）；用完必须调用 release
	Env(ctx context.Context, id int64) (repo cmdbuild.Repo, release func(), err error)
	// Maintain 每周维护：等待并独占存储位置，执行清理空间与抽样校验，之后刷新统计
	Maintain(ctx context.Context, id int64) error
	// RefreshStats 一次备份写入之后刷新统计（占用、快照数、密钥数、剩余空间）；调用方持有该存储位置的锁。
	// restic 失败时返回错误，不改动存储位置的状态。
	RefreshStats(ctx context.Context, id int64) error
}

type repoSvc struct {
	runner executor.Runner
	now    func() time.Time
	// probeLimit 保存前探测仓库是否存在的时限，测试里调短
	probeLimit time.Duration

	mu      sync.Mutex
	running map[int64]string // 正在后台进行的维护
	wg      sync.WaitGroup
}

// NewRepo 创建存储位置服务；runner 执行 restic/rclone，now 注入时钟
func NewRepo(runner executor.Runner, now func() time.Time) RepoSvc {
	return &repoSvc{runner: runner, now: now, probeLimit: existProbeTimeout, running: map[int64]string{}}
}

var defaultRepo = NewRepo(executor.New(), time.Now)

// Repo 取得存储位置服务
func Repo() RepoSvc { return defaultRepo }

// RegisterRepo 替换服务实现
func RegisterRepo(i RepoSvc) { defaultRepo = i }

// Preflight 见 RepoSvc.Preflight
func Preflight(ctx context.Context, id int64) error { return defaultRepo.Preflight(ctx, id) }

// Env 见 RepoSvc.Env
func Env(ctx context.Context, id int64) (cmdbuild.Repo, func(), error) {
	return defaultRepo.Env(ctx, id)
}

// Maintain 见 RepoSvc.Maintain
func Maintain(ctx context.Context, id int64) error { return defaultRepo.Maintain(ctx, id) }

// RefreshStats 见 RepoSvc.RefreshStats
func RefreshStats(ctx context.Context, id int64) error { return defaultRepo.RefreshStats(ctx, id) }

// wait 等待后台维护结束（测试用）
func (s *repoSvc) wait() { s.wg.Wait() }

func (s *repoSvc) find(ctx context.Context, id int64) (*repo_entity.Repo, error) {
	r, err := repo_repo.Repo().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := r.Check(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *repoSvc) List(ctx context.Context) (*repoapi.ListResponse, error) {
	list, err := repo_repo.Repo().List(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := countJobs(ctx)
	if err != nil {
		return nil, err
	}
	resp := &repoapi.ListResponse{List: make([]*repoapi.Item, 0, len(list))}
	for _, r := range list {
		resp.List = append(resp.List, s.toItem(r, counts))
	}
	return resp, nil
}

func (s *repoSvc) Get(ctx context.Context, id int64) (*repoapi.GetResponse, error) {
	item, err := s.view(ctx, id)
	if err != nil {
		return nil, err
	}
	return &repoapi.GetResponse{Item: *item}, nil
}

func (s *repoSvc) view(ctx context.Context, id int64) (*repoapi.Item, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := countJobs(ctx)
	if err != nil {
		return nil, err
	}
	return s.toItem(r, counts), nil
}

func (s *repoSvc) Create(ctx context.Context, req *repoapi.CreateRequest) (*repoapi.CreateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	ent, err := s.build(ctx, nil, req.Name, req.Type, &req.Params)
	if err != nil {
		return nil, err
	}
	if err := s.validate(ctx, ent); err != nil {
		return nil, err
	}
	wantRecovery := req.WantRecoveryKey()
	if wantRecovery {
		if err := checkRecoveryKey(ctx, req.RecoveryKeyValue, req.RecoveryKeySaved); err != nil {
			return nil, err
		}
	}
	if ent.Type == repo_entity.TypeLocal {
		if fi, err := os.Stat(ent.Params.Path); err != nil || !fi.IsDir() {
			return nil, i18n.NewError(ctx, code.RepoPathMissing, ent.Params.Path)
		}
	}
	sess, err := s.open(ctx, ent)
	if err != nil {
		return nil, err
	}
	defer sess.close()

	opsnapKey, err := newOpsnapKey()
	if err != nil {
		return nil, err
	}
	resp := &repoapi.CreateResponse{}
	var keyID string
	// 从初始化到保存记录不能因客户端断开而停下：仓库建好而 opsnap 密钥没保存，这个位置就再也打不开
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyOpTimeout)
	defer cancel()
	exists, err := s.probeRepo(ctx, ent, sess, opsnapKey)
	if err != nil {
		return nil, err
	}
	if exists {
		if keyID, err = s.adoptRepo(ctx, ent, sess, req.ExistingPassword, opsnapKey); err != nil {
			return nil, err
		}
		resp.Adopted = true
	} else if keyID, err = s.initRepo(ctx, ent, sess, opsnapKey); err != nil {
		return nil, err
	}

	now := s.now().Unix()
	if ent.OpsnapKey, err = encrypt(opsnapKey); err != nil {
		return nil, err
	}
	ent.OpsnapKeyID, ent.OpsnapKeyCreatedAt = keyID, now
	ent.KeyCount = 1
	ent.SetStatus(repo_entity.StatusOK, "", s.now())
	ent.FreeSpace = repo_entity.FreeSpaceUnknown
	ent.Createtime, ent.Updatetime = now, now

	if wantRecovery {
		// 仓库已经建好，恢复密钥没加上也照常保存，界面会显示「没有恢复密钥」，可以稍后再生成
		if id, err := s.addKey(ctx, sess, opsnapKey, req.RecoveryKeyValue); err != nil {
			resp.RecoveryKeyError = err.Error()
		} else {
			ent.RecoveryKeyID, ent.RecoveryKeyCreatedAt = id, now
			ent.KeyCount++
		}
	}
	if err := repo_repo.Repo().Create(ctx, ent); err != nil {
		return nil, err
	}
	item, err := s.view(ctx, ent.ID)
	if err != nil {
		return nil, err
	}
	resp.Item = *item
	return resp, nil
}

// initRepo 在空位置初始化仓库，返回 opsnap 密钥的 ID。本地目录必须是空目录，初始化前写入标记文件，失败时撤掉。
func (s *repoSvc) initRepo(ctx context.Context, ent *repo_entity.Repo, sess *session, opsnapKey string) (string, error) {
	wroteMarker := false
	if ent.Type == repo_entity.TypeLocal {
		empty, err := emptyDir(ent.Params.Path)
		if err != nil {
			return "", err
		}
		if !empty {
			return "", i18n.NewError(ctx, code.RepoNotEmpty, ent.Params.Path)
		}
		if wroteMarker, err = writeMarker(ent.MarkerPath()); err != nil {
			return "", err
		}
	}
	res := s.exec(ctx)(cmdbuild.ResticInit(sess.repo(opsnapKey)))
	if !res.ok() {
		if wroteMarker {
			_ = os.Remove(ent.MarkerPath())
		}
		return "", i18n.NewError(ctx, code.RepoAccessFailed, res.reason())
	}
	keys, res := s.listKeys(ctx, sess.repo(opsnapKey))
	id := currentKey(keys)
	if !res.ok() || id == "" {
		return "", i18n.NewError(ctx, code.RepoAccessFailed, "初始化后无法用 opsnap 密钥打开仓库："+res.reason())
	}
	return id, nil
}

// adoptRepo 接入已有仓库：先用现有密码验证，再加入 opsnap 密钥并验证，返回 opsnap 密钥的 ID
func (s *repoSvc) adoptRepo(ctx context.Context, ent *repo_entity.Repo, sess *session, existing, opsnapKey string) (string, error) {
	if existing == "" {
		return "", i18n.NewError(ctx, code.RepoExistingPasswordRequired)
	}
	_, res := s.listKeys(ctx, sess.repo(existing))
	if res.err == nil && res.code == exitWrongPassword {
		return "", i18n.NewError(ctx, code.RepoExistingPasswordWrong)
	}
	if !res.ok() {
		return "", i18n.NewError(ctx, code.RepoAccessFailed, res.reason())
	}
	id, err := s.addKey(ctx, sess, existing, opsnapKey)
	if err != nil {
		return "", i18n.NewError(ctx, code.RepoAccessFailed, err.Error())
	}
	if ent.Type == repo_entity.TypeLocal {
		if _, err := writeMarker(ent.MarkerPath()); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (s *repoSvc) Update(ctx context.Context, req *repoapi.UpdateRequest) (*repoapi.UpdateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	old, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	ent, err := s.build(ctx, old, req.Name, old.Type, &req.Params)
	if err != nil {
		return nil, err
	}
	if err := s.validate(ctx, ent); err != nil {
		return nil, err
	}
	if err := s.preflightErr(ctx, ent); err != nil {
		return nil, err
	}
	sess, err := s.open(ctx, ent)
	if err != nil {
		return nil, err
	}
	defer sess.close()
	key, err := decrypt(ent.OpsnapKey)
	if err != nil {
		return nil, err
	}
	if _, res := s.listKeys(ctx, sess.repo(key)); !res.ok() {
		return nil, i18n.NewError(ctx, code.RepoAccessFailed, res.reason())
	}
	ent.Updatetime = s.now().Unix()
	if err := repo_repo.Repo().UpdateConfig(ctx, ent); err != nil {
		return nil, err
	}
	item, err := s.view(ctx, ent.ID)
	if err != nil {
		return nil, err
	}
	return &repoapi.UpdateResponse{Item: *item}, nil
}

func (s *repoSvc) Delete(ctx context.Context, id int64) error {
	if _, err := s.find(ctx, id); err != nil {
		return err
	}
	counts, err := countJobs(ctx)
	if err != nil {
		return err
	}
	if n := counts[id]; n > 0 {
		return i18n.NewError(ctx, code.RepoInUse, n)
	}
	release, ok := repolock.TryAcquire(id, repolock.Exclusive)
	if !ok {
		return i18n.NewError(ctx, code.RepoBusy)
	}
	defer release()
	return repo_repo.Repo().Delete(ctx, id)
}

// build 把提交的配置合并成实体：加密新填写的凭据，占位保留 old 中已保存的值。old 为 nil 表示新建。
func (s *repoSvc) build(ctx context.Context, old *repo_entity.Repo, name, typ string, in *repoapi.Params) (*repo_entity.Repo, error) {
	ent := &repo_entity.Repo{}
	if old != nil {
		cp := *old
		ent = &cp
	} else {
		old = &repo_entity.Repo{}
	}
	ent.Name, ent.Type = strings.TrimSpace(name), typ
	p := repo_entity.Params{}
	var err error
	switch typ {
	case repo_entity.TypeLocal:
		p.Path = strings.TrimSpace(in.Path)
		ent.Credential, ent.PrivateKey = "", ""
	case repo_entity.TypeSFTP:
		p.Host, p.Port, p.Username = strings.TrimSpace(in.Host), in.Port, strings.TrimSpace(in.Username)
		p.Path, p.ServerID, p.HostKey = strings.TrimSpace(in.Path), in.ServerID, strings.TrimSpace(in.HostKey)
		// 地址、端口和经由的服务器都不变时，留空表示沿用已核对的主机公钥
		if p.HostKey == "" && old.ID != 0 && old.Params.Host == p.Host && old.Params.Port == p.Port &&
			old.Params.ServerID == p.ServerID {
			p.HostKey = old.Params.HostKey
		}
		if in.Password == secret.Masked && old.ID != 0 && (old.Params.Host != p.Host || old.Params.Port != p.Port ||
			old.Params.Username != p.Username || old.Params.ServerID != p.ServerID) {
			return nil, i18n.NewError(ctx, code.RepoCredentialRetarget, "SFTP 密码")
		}
		if ent.Credential, err = credential(ctx, "SFTP 密码", in.Password, old.Credential); err != nil {
			return nil, err
		}
		if in.PrivateKey != "" && in.PrivateKey != secret.Masked {
			if _, err := ssh.ParsePrivateKey([]byte(in.PrivateKey)); err != nil {
				return nil, i18n.NewError(ctx, code.RepoFieldInvalid, "私钥无法解析（不支持带口令的私钥）")
			}
		}
		if ent.PrivateKey, err = credential(ctx, "SFTP 私钥", in.PrivateKey, old.PrivateKey); err != nil {
			return nil, err
		}
	case repo_entity.TypeS3:
		p.Endpoint, p.Bucket, p.Prefix = strings.TrimSpace(in.Endpoint), strings.TrimSpace(in.Bucket), strings.TrimSpace(in.Prefix)
		p.Region, p.AccessKeyID, p.PathStyle = strings.TrimSpace(in.Region), strings.TrimSpace(in.AccessKeyID), in.PathStyle
		ent.PrivateKey = ""
		if ent.Credential, err = credential(ctx, "Secret Access Key", in.SecretAccessKey, old.Credential); err != nil {
			return nil, err
		}
	case repo_entity.TypeREST, repo_entity.TypeWebDAV:
		p.URL, p.Username = strings.TrimSpace(in.URL), strings.TrimSpace(in.Username)
		if typ == repo_entity.TypeWebDAV {
			p.Path, p.Vendor = strings.TrimSpace(in.Path), strings.TrimSpace(in.Vendor)
		}
		ent.PrivateKey = ""
		// 密码以 Basic 认证原样发给对方：地址或用户改了就不能沿用已保存的
		if in.Password == secret.Masked && old.ID != 0 && (old.Params.URL != p.URL || old.Params.Username != p.Username) {
			return nil, i18n.NewError(ctx, code.RepoCredentialRetarget, "密码")
		}
		if ent.Credential, err = credential(ctx, "密码", in.Password, old.Credential); err != nil {
			return nil, err
		}
	default:
		return nil, i18n.NewError(ctx, code.RepoTypeInvalid)
	}
	ent.Params = p
	return ent, nil
}

// validate 实体校验之外，SFTP 还要校验主机公钥格式和经由的服务器
func (s *repoSvc) validate(ctx context.Context, ent *repo_entity.Repo) error {
	if err := ent.Validate(ctx); err != nil {
		return err
	}
	if ent.Type != repo_entity.TypeSFTP {
		return nil
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ent.Params.HostKey)); err != nil {
		return i18n.NewError(ctx, code.RepoHostKeyInvalid)
	}
	if ent.Params.ServerID != 0 {
		srv, err := server_repo.Server().Find(ctx, ent.Params.ServerID)
		if err != nil {
			return err
		}
		if srv == nil {
			return i18n.NewError(ctx, code.RepoServerNotFound)
		}
	}
	return nil
}

// credential 处理一项凭据：空串表示没有，占位保留已保存的密文，其余加密保存
func credential(ctx context.Context, label, input, saved string) (string, error) {
	switch input {
	case "":
		return "", nil
	case secret.Masked:
		if saved == "" {
			return "", i18n.NewError(ctx, code.RepoCredentialMissing, label)
		}
		return saved, nil
	}
	return encrypt(input)
}

func encrypt(plain string) (string, error) {
	sec := secret.Default()
	if sec == nil {
		return "", errors.New("主密钥未加载，无法加密存储位置凭据")
	}
	return sec.Encrypt(plain)
}

func decrypt(cipher string) (string, error) {
	if cipher == "" {
		return "", nil
	}
	sec := secret.Default()
	if sec == nil {
		return "", errors.New("主密钥未加载，无法解密存储位置凭据")
	}
	return sec.Decrypt(cipher)
}

// toItem 转换成接口返回的结构；凭据只返回占位
func (s *repoSvc) toItem(r *repo_entity.Repo, counts map[int64]int) *repoapi.Item {
	p := r.Params
	item := &repoapi.Item{
		ID:   r.ID,
		Name: r.Name,
		Type: r.Type,
		Params: repoapi.Params{
			Path: p.Path, Host: p.Host, Port: p.Port, Username: p.Username, HostKey: p.HostKey, ServerID: p.ServerID,
			Endpoint: p.Endpoint, Bucket: p.Bucket, Prefix: p.Prefix, Region: p.Region, AccessKeyID: p.AccessKeyID,
			PathStyle: p.PathStyle, URL: p.URL, Vendor: p.Vendor,
			PrivateKey: secret.Mask(r.PrivateKey),
		},
		Address:          r.Address(),
		Encryption:       "AES-256",
		Status:           r.Status,
		StatusMessage:    r.StatusMessage,
		StatusAt:         r.StatusAt,
		StoredSize:       r.StoredSize,
		RawSize:          r.RawSize,
		DedupRatio:       r.DedupRatio(),
		SnapshotCount:    r.SnapshotCount,
		KeyCount:         r.KeyCount,
		StatsAt:          r.StatsAt,
		NoRecoveryKey:    !r.HasRecoveryKey(),
		LastCheckAt:      r.LastCheckAt,
		LastCheckOK:      r.LastCheckOK,
		LastCheckMessage: r.LastCheckMessage,
		LastPruneAt:      r.LastPruneAt,
		LastPruneOK:      r.LastPruneOK,
		LastPruneMessage: r.LastPruneMessage,
		Maintenance:      s.runningOf(r.ID),
		JobCount:         counts[r.ID],
		CreatedAt:        r.Createtime,
		UpdatedAt:        r.Updatetime,
	}
	if item.Status == "" {
		item.Status = repo_entity.StatusUnknown
	}
	if r.Type == repo_entity.TypeS3 {
		item.Params.SecretAccessKey = secret.Mask(r.Credential)
	} else {
		item.Params.Password = secret.Mask(r.Credential)
	}
	if r.FreeSpace >= 0 && r.StatsAt != 0 {
		v := r.FreeSpace
		item.FreeSpace = &v
	}
	return item
}

func (s *repoSvc) runningOf(id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func (s *repoSvc) setRunning(id int64, action string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if action == "" {
		delete(s.running, id)
		return
	}
	s.running[id] = action
}

// Env 见 RepoSvc.Env
func (s *repoSvc) Env(ctx context.Context, id int64) (cmdbuild.Repo, func(), error) {
	ent, err := s.find(ctx, id)
	if err != nil {
		return cmdbuild.Repo{}, nil, err
	}
	key, err := decrypt(ent.OpsnapKey)
	if err != nil {
		return cmdbuild.Repo{}, nil, err
	}
	sess, err := s.open(ctx, ent)
	if err != nil {
		return cmdbuild.Repo{}, nil, err
	}
	return sess.repo(key), sess.close, nil
}

// SFTPHostKey 见 RepoSvc.SFTPHostKey：只做到密钥交换，不发送任何凭据
func (s *repoSvc) SFTPHostKey(ctx context.Context, req *repoapi.SFTPHostKeyRequest) (*repoapi.SFTPHostKeyResponse, error) {
	host := strings.TrimSpace(req.Host)
	port := req.Port
	if port == 0 {
		port = 22
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n/") || port < 1 || port > 65535 {
		return nil, i18n.NewError(ctx, code.RepoFieldInvalid, "SFTP 主机或端口不合法")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var conn net.Conn
	if req.ServerID != 0 {
		via, _, err := sshconn.Dial(ctx, req.ServerID)
		if errors.Is(err, sshconn.ErrServerNotFound) {
			return nil, i18n.NewError(ctx, code.RepoServerNotFound)
		}
		if err != nil {
			return nil, i18n.NewError(ctx, code.RepoHostKeyProbeFailed, err.Error())
		}
		defer func() { _ = via.Close() }()
		if conn, err = via.Client().DialContext(ctx, "tcp", addr); err != nil {
			return nil, i18n.NewError(ctx, code.RepoHostKeyProbeFailed, err.Error())
		}
	} else {
		var d net.Dialer
		var err error
		if conn, err = d.DialContext(ctx, "tcp", addr); err != nil {
			return nil, i18n.NewError(ctx, code.RepoHostKeyProbeFailed, err.Error())
		}
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(probeTimeout))

	var got ssh.PublicKey
	_, _, _, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: "opsnap",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errProbeDone
		},
	})
	if got == nil {
		msg := "SSH 握手失败"
		if err != nil {
			msg = err.Error()
		}
		return nil, i18n.NewError(ctx, code.RepoHostKeyProbeFailed, msg)
	}
	return &repoapi.SFTPHostKeyResponse{
		Fingerprint: ssh.FingerprintSHA256(got),
		HostKey:     strings.TrimSpace(string(ssh.MarshalAuthorizedKey(got))),
	}, nil
}

func (s *repoSvc) Keys(ctx context.Context, id int64) (*repoapi.KeysResponse, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	return &repoapi.KeysResponse{Keys: keysOf(r)}, nil
}

// RotateKey 见 RepoSvc.RotateKey。顺序：加新密钥并验证 → 保存新密钥 → 用新密钥删旧密钥。
// 删旧密钥失败时恢复保存的旧密钥并撤掉新密钥；任何一步失败，仓库里的密钥和保存的 opsnap 密钥都保持原状。
func (s *repoSvc) RotateKey(ctx context.Context, id int64) (*repoapi.RotateKeyResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	// 多步操作中途不能因客户端断开而停下：旧密钥可能已在仓库里删掉，这时再恢复保存的旧密钥就再也打不开仓库
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyOpTimeout)
	defer cancel()
	sess, oldKey, done, err := s.keyOp(ctx, r)
	if err != nil {
		return nil, err
	}
	defer done()
	fail := func(reason string) error {
		return i18n.NewError(ctx, code.RepoKeyOperationFailed, "更换 opsnap 密钥", reason)
	}

	newKey, err := newOpsnapKey()
	if err != nil {
		return nil, err
	}
	newID, err := s.addKey(ctx, sess, oldKey, newKey)
	if err != nil {
		return nil, fail(err.Error())
	}
	prev := *r
	if r.OpsnapKey, err = encrypt(newKey); err != nil {
		s.removeKey(ctx, sess.repo(oldKey), newID)
		return nil, err
	}
	r.OpsnapKeyID, r.OpsnapKeyCreatedAt = newID, s.now().Unix()
	if err := repo_repo.Repo().UpdateKeys(ctx, r); err != nil {
		s.removeKey(ctx, sess.repo(oldKey), newID)
		return nil, fail("保存新密钥：" + err.Error())
	}
	res := s.exec(ctx)(cmdbuild.ResticKeyRemove(sess.repo(newKey), prev.OpsnapKeyID))
	if !res.ok() {
		// 先恢复保存的旧密钥，再撤掉新密钥；恢复不了就保留新密钥，两把都能打开仓库，保存的新密钥仍可用
		if err := repo_repo.Repo().UpdateKeys(context.WithoutCancel(ctx), &prev); err != nil {
			return nil, fail(fmt.Sprintf("删除旧密钥：%s；恢复原密钥记录也失败：%v，已改用新密钥，旧密钥仍在仓库中", res.reason(), err))
		}
		s.removeKey(ctx, sess.repo(oldKey), newID)
		return nil, fail("删除旧密钥：" + res.reason())
	}
	return &repoapi.RotateKeyResponse{Keys: keysOf(r)}, nil
}

// checkRecoveryKey 加入恢复密钥前的校验：必须提交合法的值，并确认已存进密码管理器。在访问仓库之前调用。
func checkRecoveryKey(ctx context.Context, value string, saved bool) error {
	if !repo_entity.ValidRecoveryKey(value) {
		return i18n.NewError(ctx, code.RepoRecoveryKeyInvalid, repo_entity.MinRecoveryKeyLen)
	}
	if !saved {
		return i18n.NewError(ctx, code.RepoRecoveryKeyNotSaved)
	}
	return nil
}

// NewRecoveryKey 见 RepoSvc.NewRecoveryKey
func (s *repoSvc) NewRecoveryKey(ctx context.Context) (*repoapi.NewRecoveryKeyResponse, error) {
	v, err := newRecoveryKey()
	if err != nil {
		return nil, err
	}
	return &repoapi.NewRecoveryKeyResponse{RecoveryKeyValue: v}, nil
}

// GenerateRecoveryKey 见 RepoSvc.GenerateRecoveryKey
func (s *repoSvc) GenerateRecoveryKey(ctx context.Context, req *repoapi.GenerateRecoveryKeyRequest) (*repoapi.GenerateRecoveryKeyResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	r, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if r.HasRecoveryKey() {
		return nil, i18n.NewError(ctx, code.RepoRecoveryKeyExists)
	}
	if err := checkRecoveryKey(ctx, req.RecoveryKeyValue, req.RecoveryKeySaved); err != nil {
		return nil, err
	}
	sess, opsnapKey, done, err := s.keyOp(ctx, r)
	if err != nil {
		return nil, err
	}
	defer done()
	if r.HasRecoveryKey() {
		return nil, i18n.NewError(ctx, code.RepoRecoveryKeyExists)
	}
	keyID, err := s.addKey(ctx, sess, opsnapKey, req.RecoveryKeyValue)
	if err != nil {
		return nil, i18n.NewError(ctx, code.RepoKeyOperationFailed, "生成恢复密钥", err.Error())
	}
	r.RecoveryKeyID, r.RecoveryKeyCreatedAt = keyID, s.now().Unix()
	r.KeyCount++
	if err := repo_repo.Repo().UpdateKeys(ctx, r); err != nil {
		s.removeKey(ctx, sess.repo(opsnapKey), keyID)
		return nil, err
	}
	return &repoapi.GenerateRecoveryKeyResponse{Keys: keysOf(r)}, nil
}

// RemoveRecoveryKey 见 RepoSvc.RemoveRecoveryKey
func (s *repoSvc) RemoveRecoveryKey(ctx context.Context, id int64) (*repoapi.RemoveRecoveryKeyResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if !r.HasRecoveryKey() {
		return nil, i18n.NewError(ctx, code.RepoRecoveryKeyMissing)
	}
	sess, opsnapKey, done, err := s.keyOp(ctx, r)
	if err != nil {
		return nil, err
	}
	defer done()
	if !r.HasRecoveryKey() {
		return nil, i18n.NewError(ctx, code.RepoRecoveryKeyMissing)
	}
	if res := s.exec(ctx)(cmdbuild.ResticKeyRemove(sess.repo(opsnapKey), r.RecoveryKeyID)); !res.ok() {
		return nil, i18n.NewError(ctx, code.RepoKeyOperationFailed, "移除恢复密钥", res.reason())
	}
	r.RecoveryKeyID, r.RecoveryKeyCreatedAt = "", 0
	r.KeyCount = max(r.KeyCount-1, 1)
	if err := repo_repo.Repo().UpdateKeys(ctx, r); err != nil {
		return nil, err
	}
	return &repoapi.RemoveRecoveryKeyResponse{Keys: keysOf(r)}, nil
}

// RecoveryBundle 见 RepoSvc.RecoveryBundle。只写仓库地址、凭据的占位和命令，不写任何密钥、密码或密文。
func (s *repoSvc) RecoveryBundle(ctx context.Context, id int64) (*repoapi.RecoveryBundleResponse, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	return &repoapi.RecoveryBundleResponse{
		Filename: fmt.Sprintf("opsnap-recovery-%d.txt", r.ID),
		Content:  bundleContent(r, s.now()),
	}, nil
}

// Preflight 见 RepoSvc.Preflight
func (s *repoSvc) Preflight(ctx context.Context, id int64) error {
	r, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	return s.preflight(ctx, r)
}

// RefreshStats 见 RepoSvc.RefreshStats
func (s *repoSvc) RefreshStats(ctx context.Context, id int64) error {
	r, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	return s.refreshStats(ctx, r)
}

// Recheck 见 RepoSvc.Recheck。没有挂载时不运行 restic，直接返回标红的状态。
func (s *repoSvc) Recheck(ctx context.Context, id int64) (*repoapi.RecheckResponse, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.preflight(ctx, r); err == nil {
		release, ok := repolock.TryAcquire(id, repolock.Shared)
		if !ok {
			return nil, i18n.NewError(ctx, code.RepoBusy)
		}
		err := s.refresh(ctx, r)
		release()
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, ErrNotMounted) {
		return nil, err
	}
	item, err := s.view(ctx, id)
	if err != nil {
		return nil, err
	}
	return &repoapi.RecheckResponse{Item: *item}, nil
}

// StartCheck 见 RepoSvc.StartCheck
func (s *repoSvc) StartCheck(ctx context.Context, id int64) (*repoapi.CheckResponse, error) {
	item, err := s.start(ctx, id, repoapi.MaintenanceCheck)
	if err != nil {
		return nil, err
	}
	return &repoapi.CheckResponse{Item: *item}, nil
}

// StartPrune 见 RepoSvc.StartPrune
func (s *repoSvc) StartPrune(ctx context.Context, id int64) (*repoapi.PruneResponse, error) {
	item, err := s.start(ctx, id, repoapi.MaintenancePrune)
	if err != nil {
		return nil, err
	}
	return &repoapi.PruneResponse{Item: *item}, nil
}

// Maintain 见 RepoSvc.Maintain：等待独占锁（ctx 结束则放弃），先清理空间再抽样校验，两项都记录结果
func (s *repoSvc) Maintain(ctx context.Context, id int64) error {
	if _, err := s.find(ctx, id); err != nil {
		return err
	}
	release, err := repolock.Acquire(ctx, id, repolock.Exclusive)
	if err != nil {
		return err
	}
	defer release()
	// 排队期间密钥可能被更换或位置被修改，拿到锁后重新读取
	r, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	if err := s.preflight(ctx, r); err != nil {
		return err
	}
	s.setRunning(id, repoapi.MaintenancePrune)
	pruneErr := s.maintain(ctx, r, repoapi.MaintenancePrune)
	s.setRunning(id, repoapi.MaintenanceCheck)
	checkErr := s.maintain(ctx, r, repoapi.MaintenanceCheck)
	s.setRunning(id, "")
	// 清理空间会改变占用，统计随之刷新；刷新失败不影响维护结果
	_ = s.refreshStats(ctx, r)
	return errors.Join(pruneErr, checkErr)
}

// Unlock 见 RepoSvc.Unlock：独占存储位置后执行 restic unlock --remove-all
func (s *repoSvc) Unlock(ctx context.Context, id int64, confirm bool) error {
	r, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	if !confirm {
		return i18n.NewError(ctx, code.RepoUnlockNotConfirmed)
	}
	release, ok := repolock.TryAcquire(id, repolock.Exclusive)
	if !ok {
		return i18n.NewError(ctx, code.RepoBusy)
	}
	defer release()
	if err := s.preflightErr(ctx, r); err != nil {
		return err
	}
	key, err := decrypt(r.OpsnapKey)
	if err != nil {
		return err
	}
	sess, err := s.open(ctx, r)
	if err != nil {
		return err
	}
	defer sess.close()
	if res := s.exec(ctx)(cmdbuild.ResticUnlock(sess.repo(key), true)); !res.ok() {
		return i18n.NewError(ctx, code.RepoUnlockFailed, res.reason())
	}
	return nil
}
