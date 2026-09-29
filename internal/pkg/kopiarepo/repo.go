package kopiarepo

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/blob/s3"
	"github.com/kopia/kopia/repo/encryption"
	"github.com/kopia/kopia/repo/format"
	"github.com/kopia/kopia/snapshot"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

var (
	// ErrInvalidPassword 密钥打不开仓库
	ErrInvalidPassword error = l10n.Errorf(code.KopiaErrInvalidPassword)
	// ErrNotEmpty 建库时目标位置不为空，也不是 kopia 仓库
	ErrNotEmpty error = l10n.Errorf(code.KopiaErrNotEmpty)
	// ErrAlreadyRepository 建库时目标位置已是 kopia 仓库
	ErrAlreadyRepository error = l10n.Errorf(code.KopiaErrAlreadyRepository)
	// ErrNotRepository 连接时目标位置不是 kopia 仓库
	ErrNotRepository error = l10n.Errorf(code.KopiaErrNotRepository)
)

// Encryption 新建仓库使用的加密算法
const Encryption = encryption.DefaultAlgorithm

// tmpVerifyPrefix 新建存储尚未保存时，校验所用一次性目录的前缀
const tmpVerifyPrefix = "tmp-verify-"

// Manager 管理每个存储在本机的 kopia 连接配置（只在校验期间存在），目录为 <root>/<存储 ID>/
type Manager struct {
	root string

	mu sync.Mutex
	// locks 每个存储一把锁：同一存储的连接配置同一时间只由一个操作改写
	locks map[int64]*sync.Mutex
	// maintLocks 每个位置一把锁：每个写入会话的配置目录不同，kopia 在配置旁的维护锁管不到其他会话
	maintLocks map[string]*sync.Mutex
}

// configName 校验时 kopia 写入的连接配置文件名，其中有明文的存储凭据
const configName = "repository.config"

// NewManager root 通常为 <数据目录>/kopia。
// 启动时清理上次进程在校验或写入途中退出留下的连接配置，不让其中的明文凭据留在磁盘上。
func NewManager(root string) *Manager {
	// 逐项列目录而不用 Glob：数据目录名中的 [ ] * ? 会被 Glob 当作通配符，导致什么也清不掉
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name())
		if strings.HasPrefix(e.Name(), tmpVerifyPrefix) || strings.HasPrefix(e.Name(), tmpWritePrefix) {
			_ = os.RemoveAll(p)
			continue
		}
		_ = os.Remove(filepath.Join(p, configName))
	}
	return &Manager{root: root, locks: map[int64]*sync.Mutex{}, maintLocks: map[string]*sync.Mutex{}}
}

func (m *Manager) dir(id int64) string {
	return filepath.Join(m.root, strconv.FormatInt(id, 10))
}

// lock 锁住该存储的本机目录，返回解锁函数
func (m *Manager) lock(id int64) func() {
	return lockIn(&m.mu, m.locks, id)
}

// lockIn 取出（没有则建立）locks 中 key 对应的锁并锁住，返回解锁函数；mu 保护 locks
func lockIn[K comparable](mu *sync.Mutex, locks map[K]*sync.Mutex, key K) func() {
	mu.Lock()
	l, ok := locks[key]
	if !ok {
		l = &sync.Mutex{}
		locks[key] = l
	}
	mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Create 用密钥作为密码在空位置创建加密的 kopia 仓库；本地目录不存在时以 0700 创建。
// 建库前重新探测：位置已不为空或已是仓库时放弃（ErrNotEmpty / ErrAlreadyRepository），不写入任何数据。
func Create(ctx context.Context, loc Location, password string) error {
	res, err := Probe(ctx, loc)
	if err != nil {
		return err
	}
	switch res.State {
	case StateNotEmpty:
		return ErrNotEmpty
	case StateRepository:
		return ErrAlreadyRepository
	case StateEmpty:
	}
	if loc.Kind == KindLocal {
		if err := os.MkdirAll(loc.Path, 0o700); err != nil {
			return locErr(ReasonNotWritable, err)
		}
	}
	st, err := openStorage(ctx, loc, true)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(ctx) }()
	err = repo.Initialize(ctx, st, &repo.NewRepositoryOptions{
		BlockFormat: format.ContentFormat{Encryption: Encryption},
	}, password)
	if errors.Is(err, repo.ErrAlreadyInitialized) {
		return ErrAlreadyRepository
	}
	if err != nil {
		return l10n.Errorf(code.KopiaCreate, err)
	}
	return nil
}

// Verify 用密钥以只读方式连接并打开仓库，返回其中的快照数。仓库本身不做任何改动。
// id 为存储 ID，连接配置临时放在该存储的目录下、校验结束即删除，同一存储的校验依次进行；
// id 为 0 时用一次性目录，用完即删（新建存储尚未保存时）。
func (m *Manager) Verify(ctx context.Context, id int64, loc Location, password string) (int, error) {
	var dir string
	if id == 0 {
		if err := os.MkdirAll(m.root, 0o700); err != nil {
			return 0, err
		}
		tmp, err := os.MkdirTemp(m.root, tmpVerifyPrefix)
		if err != nil {
			return 0, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dir = tmp
	} else {
		defer m.lock(id)()
		dir = m.dir(id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return 0, err
		}
	}
	cfg := filepath.Join(dir, configName)
	// 每次都按当前参数与密钥重新连接，避免沿用旧位置或旧密钥
	if err := os.Remove(cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	// 连接配置里有明文的存储凭据（如 S3 Secret Key），用完即删，不留在磁盘上
	defer func() { _ = os.Remove(cfg) }()

	r, err := connectAndOpen(ctx, cfg, loc, password, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = r.Close(ctx) }()
	ids, err := snapshot.ListSnapshotManifests(ctx, r, nil, nil)
	if err != nil {
		return 0, l10n.Errorf(code.KopiaListSnapshots, err)
	}
	return len(ids), nil
}

// connectAndOpen 把连接配置写到 cfg 并打开仓库。cfg 中有明文的存储凭据，由调用方在用完后删除。
// 仓库日志关闭，打开本身不向仓库写入任何内容；客户端身份固定为 opsnap@opsnap，不随本机主机名变化。
func connectAndOpen(ctx context.Context, cfg string, loc Location, password string, readOnly bool) (repo.Repository, error) {
	st, err := openStorage(ctx, loc, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close(ctx) }()
	err = repo.Connect(ctx, cfg, st, password, &repo.ConnectOptions{
		ClientOptions: repo.ClientOptions{ReadOnly: readOnly, Hostname: sourceHost, Username: sourceUser},
	})
	if err != nil {
		return nil, connectError(err)
	}
	r, err := repo.Open(ctx, cfg, password, &repo.Options{
		DisableRepositoryLog: true,
		// 默认会 os.Exit
		OnFatalError: func(error) {},
	})
	if err != nil {
		return nil, connectError(err)
	}
	return r, nil
}

// Remove 清理该存储在本机的 kopia 配置；不触碰存储中的数据
func (m *Manager) Remove(id int64) error {
	defer m.lock(id)()
	return os.RemoveAll(m.dir(id))
}

func connectError(err error) error {
	switch {
	case errors.Is(err, repo.ErrInvalidPassword):
		return ErrInvalidPassword
	case errors.Is(err, repo.ErrRepositoryNotInitialized), errors.Is(err, blob.ErrBlobNotFound):
		return ErrNotRepository
	case errors.Is(err, blob.ErrInvalidCredentials):
		return locErr(ReasonAccessDenied, err)
	}
	return l10n.Errorf(code.KopiaConnect, err)
}

// openStorage 按位置得到 kopia 存储后端
func openStorage(ctx context.Context, loc Location, isCreate bool) (blob.Storage, error) {
	switch loc.Kind {
	case KindLocal:
		fi, err := os.Stat(loc.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, ErrNotRepository
			}
			return nil, locErr(ReasonNoAccess, err)
		}
		if !fi.IsDir() {
			return nil, locErr(ReasonNotDirectory, l10n.Errorf(code.KopiaNotDirectory, loc.Path))
		}
		// kopia 的 filesystem 后端一经访问就会写入 .shards，连接前先确认确实是仓库，避免改动非仓库目录
		if _, ok := localRepository(loc.Path); !ok && !isCreate {
			return nil, ErrNotRepository
		}
		st, err := filesystem.New(ctx, &filesystem.Options{Path: loc.Path}, isCreate)
		if err != nil {
			return nil, locErr(ReasonUnreachable, err)
		}
		return st, nil
	case KindS3:
		st, err := s3.New(ctx, &s3.Options{
			BucketName:      loc.Bucket,
			Prefix:          loc.Prefix,
			Endpoint:        loc.Endpoint,
			Region:          loc.Region,
			DoNotUseTLS:     !loc.UseTLS,
			DoNotVerifyTLS:  loc.SkipVerify,
			AccessKeyID:     loc.AccessKey,
			SecretAccessKey: loc.SecretKey,
		}, isCreate)
		if err != nil {
			return nil, s3Error(err)
		}
		return st, nil
	default:
		return nil, ErrUnknownKind
	}
}
