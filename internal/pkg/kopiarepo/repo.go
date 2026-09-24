package kopiarepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/blob/s3"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/encryption"
	"github.com/kopia/kopia/repo/format"
	"github.com/kopia/kopia/snapshot"
)

var (
	// ErrInvalidPassword 密钥打不开仓库
	ErrInvalidPassword = errors.New("密钥不正确，无法解开这个仓库")
	// ErrNotEmpty 建库时目标位置不为空，也不是 kopia 仓库
	ErrNotEmpty = errors.New("目标位置不为空，且不是 kopia 仓库")
	// ErrAlreadyRepository 建库时目标位置已是 kopia 仓库
	ErrAlreadyRepository = errors.New("目标位置已是 kopia 仓库")
	// ErrNotRepository 连接时目标位置不是 kopia 仓库
	ErrNotRepository = errors.New("目标位置不是 kopia 仓库")
)

// Encryption 新建仓库使用的加密算法
const Encryption = encryption.DefaultAlgorithm

// Manager 管理每个存储在本机的 kopia 配置与缓存，目录为 <root>/<存储 ID>/
type Manager struct {
	root string
}

// NewManager root 通常为 <数据目录>/kopia
func NewManager(root string) *Manager {
	return &Manager{root: root}
}

func (m *Manager) dir(id int64) string {
	return filepath.Join(m.root, strconv.FormatInt(id, 10))
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
		return fmt.Errorf("创建 kopia 仓库: %w", err)
	}
	return nil
}

// Verify 用密钥连接并打开仓库，返回其中的快照数。仓库本身不做任何改动。
// id 为存储 ID，连接配置与缓存保存在该存储的目录下；id 为 0 时用一次性目录，用完即删（新建存储尚未保存时）。
func (m *Manager) Verify(ctx context.Context, id int64, loc Location, password string) (int, error) {
	var dir string
	if id == 0 {
		if err := os.MkdirAll(m.root, 0o700); err != nil {
			return 0, err
		}
		tmp, err := os.MkdirTemp(m.root, "tmp-verify-")
		if err != nil {
			return 0, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dir = tmp
	} else {
		dir = m.dir(id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return 0, err
		}
	}
	cfg := filepath.Join(dir, "repository.config")
	// 每次都按当前参数与密钥重新连接，避免沿用旧位置或旧密钥
	if err := os.Remove(cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}

	st, err := openStorage(ctx, loc, false)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close(ctx) }()
	err = repo.Connect(ctx, cfg, st, password, &repo.ConnectOptions{
		CachingOptions: content.CachingOptions{CacheDirectory: filepath.Join(dir, "cache")},
	})
	if err != nil {
		return 0, connectError(err)
	}

	r, err := repo.Open(ctx, cfg, password, &repo.Options{
		DisableRepositoryLog: true,
		OnFatalError:         func(error) {},
	})
	if err != nil {
		return 0, connectError(err)
	}
	defer func() { _ = r.Close(ctx) }()
	ids, err := snapshot.ListSnapshotManifests(ctx, r, nil, nil)
	if err != nil {
		return 0, fmt.Errorf("读取快照列表: %w", err)
	}
	return len(ids), nil
}

// Remove 清理该存储在本机的 kopia 配置与缓存；不触碰存储中的数据
func (m *Manager) Remove(id int64) error {
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
	return fmt.Errorf("连接 kopia 仓库: %w", err)
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
			return nil, locErr(ReasonNotDirectory, fmt.Errorf("%s 不是目录", loc.Path))
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
