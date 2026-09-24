// Package storage_svc 实现存储的增删改、测试连接与解锁。每个存储对应一个 kopia 仓库，
// 仓库密钥与 S3 Secret Key 用主密钥加密后保存；已有数据只读不改（docs/specs/2026-09-24-storage.md）。
package storage_svc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	api "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

const (
	maxNameLength = 64
	// minKeyLength 自设密码的最短长度
	minKeyLength = 12
	// opTimeout 一次探测、建库或连接的最长时间
	opTimeout = 2 * time.Minute
)

type StorageSvc interface {
	List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error)
	Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error)
	Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error)
	Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error)
	Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error)
	Unlock(ctx context.Context, req *api.UnlockRequest) (*api.UnlockResponse, error)
	Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error)
	Key(ctx context.Context, req *api.KeyRequest) (*api.KeyResponse, error)
	// Reveal 再次验证身份后返回仓库密钥；调用方保证是浏览器会话
	Reveal(ctx context.Context, req *api.RevealRequest, meta auth_svc.ClientMeta) (*api.RevealResponse, error)
	ListDirs(ctx context.Context, req *api.ListDirsRequest) (*api.ListDirsResponse, error)
	MakeDir(ctx context.Context, req *api.MakeDirRequest) (*api.MakeDirResponse, error)
}

type storageSvc struct {
	now func() time.Time

	mu    sync.RWMutex
	kopia *kopiarepo.Manager
	// browseStart 目录浏览的默认位置：数据目录的上级目录
	browseStart string
}

var defaultStorage = &storageSvc{now: time.Now}

func Storage() StorageSvc {
	return defaultStorage
}

// SetDataDir 启动时调用：各存储的 kopia 配置与缓存放在 <数据目录>/kopia，目录浏览默认打开数据目录的上级目录
func SetDataDir(dir string) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	defaultStorage.mu.Lock()
	defer defaultStorage.mu.Unlock()
	defaultStorage.kopia = kopiarepo.NewManager(filepath.Join(dir, "kopia"))
	defaultStorage.browseStart = filepath.Dir(dir)
}

func (s *storageSvc) manager() *kopiarepo.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kopia
}

// detailCodes 文案中带 %s 详情的错误码
var detailCodes = map[int]bool{
	code.StorageUnreachable:  true,
	code.StorageNotWritable:  true,
	code.StorageNotDirectory: true,
	code.StorageNoAccess:     true,
	code.StorageAccessDenied: true,
}

// classify 把仓库层错误归为错误码与详情；未识别的错误归为“无法连接”并附原文
func classify(err error) (c int, detail string) {
	var le *kopiarepo.LocationError
	switch {
	case errors.As(err, &le):
		switch le.Reason {
		case kopiarepo.ReasonNotDirectory:
			c = code.StorageNotDirectory
		case kopiarepo.ReasonNotWritable:
			c = code.StorageNotWritable
		case kopiarepo.ReasonNoAccess:
			c = code.StorageNoAccess
		case kopiarepo.ReasonBucketNotFound:
			c = code.StorageBucketNotFound
		case kopiarepo.ReasonAccessDenied:
			c = code.StorageAccessDenied
		default:
			c = code.StorageUnreachable
		}
	case errors.Is(err, kopiarepo.ErrNotEmpty):
		c = code.StorageLocationNotEmpty
	case errors.Is(err, kopiarepo.ErrAlreadyRepository):
		c = code.StorageAlreadyRepository
	case errors.Is(err, kopiarepo.ErrNotRepository):
		c = code.StorageNotRepository
	case errors.Is(err, kopiarepo.ErrInvalidPassword):
		c = code.StorageKeyInvalid
	default:
		c = code.StorageUnreachable
	}
	if detailCodes[c] {
		detail = err.Error()
	}
	return c, detail
}

func codeError(ctx context.Context, c int, detail string) error {
	if detailCodes[c] {
		return i18n.NewError(ctx, c, detail)
	}
	return i18n.NewError(ctx, c)
}

// repoError 把仓库层错误转为接口错误
func repoError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	c, detail := classify(err)
	return codeError(ctx, c, detail)
}

func (s *storageSvc) toItem(ctx context.Context, st *storage_entity.Storage) *api.Item {
	item := &api.Item{
		ID:            st.ID,
		Name:          st.Name,
		Kind:          st.Kind,
		Path:          st.Path,
		Endpoint:      st.Endpoint,
		Region:        st.Region,
		Bucket:        st.Bucket,
		Prefix:        st.Prefix,
		AccessKey:     st.AccessKey,
		HasSecretKey:  st.SecretKey != "",
		UseTLS:        st.UseTLS,
		SkipVerify:    st.SkipVerify,
		Location:      st.Location("").String(),
		Fingerprint:   st.Fingerprint,
		Encryption:    kopiarepo.Encryption,
		Status:        st.Status,
		CheckedAt:     st.Checktime,
		CreatedAt:     st.Createtime,
		StatusMessage: "",
	}
	if st.StatusCode != 0 {
		if detailCodes[st.StatusCode] {
			item.StatusMessage = i18n.T(ctx, st.StatusCode, st.StatusDetail)
		} else {
			item.StatusMessage = i18n.T(ctx, st.StatusCode)
		}
	}
	return item
}

// location 校验名称与位置参数，返回规范化后的位置。existing 为正在编辑的存储（新建时为 nil）：
// 唯一性检查排除它自己，S3 Secret Key 留空时沿用它保存的值
func (s *storageSvc) location(ctx context.Context, name string, in api.Location, existing *storage_entity.Storage) (string, kopiarepo.Location, error) {
	var loc kopiarepo.Location
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return "", loc, i18n.NewError(ctx, code.StorageNameInvalid)
	}
	loc = kopiarepo.Location{
		Kind:       kopiarepo.Kind(in.Kind),
		Path:       strings.TrimSpace(in.Path),
		Endpoint:   in.Endpoint,
		Region:     strings.TrimSpace(in.Region),
		Bucket:     in.Bucket,
		Prefix:     in.Prefix,
		AccessKey:  strings.TrimSpace(in.AccessKey),
		SecretKey:  in.SecretKey,
		UseTLS:     in.UseTLS,
		SkipVerify: in.SkipVerify,
	}
	if loc.Kind == kopiarepo.KindS3 && loc.SecretKey == "" && existing != nil && existing.SecretKey != "" {
		secretKey, err := secret_svc.Secret().Decrypt(ctx, existing.SecretKey)
		if err != nil {
			return "", loc, err
		}
		loc.SecretKey = secretKey
	}
	if err := loc.Normalize(); err != nil {
		switch {
		case errors.Is(err, kopiarepo.ErrRelativePath):
			return "", loc, i18n.NewError(ctx, code.StoragePathRelative)
		case errors.Is(err, kopiarepo.ErrEndpointScheme):
			return "", loc, i18n.NewError(ctx, code.StorageEndpointScheme)
		}
		return "", loc, err
	}
	if loc.Kind == kopiarepo.KindS3 && (loc.Endpoint == "" || loc.Bucket == "" || loc.AccessKey == "" || loc.SecretKey == "") {
		return "", loc, i18n.NewError(ctx, code.StorageS3FieldRequired)
	}

	var selfID int64
	if existing != nil {
		selfID = existing.ID
	}
	same, err := storage_repo.Storage().FindByName(ctx, name)
	if err != nil {
		return "", loc, err
	}
	if same != nil && same.ID != selfID {
		return "", loc, i18n.NewError(ctx, code.StorageNameDuplicate)
	}
	same, err = storage_repo.Storage().FindByLocationKey(ctx, loc.Key())
	if err != nil {
		return "", loc, err
	}
	if same != nil && same.ID != selfID {
		return "", loc, i18n.NewError(ctx, code.StorageLocationInUse, same.Name)
	}
	return name, loc, nil
}

func (s *storageSvc) probe(ctx context.Context, loc kopiarepo.Location) (*kopiarepo.ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	res, err := kopiarepo.Probe(ctx, loc)
	if err != nil {
		return nil, repoError(ctx, err)
	}
	return res, nil
}

// verify 用密钥打开仓库并返回快照数；id 为 0 时不保留连接配置
func (s *storageSvc) verify(ctx context.Context, id int64, loc kopiarepo.Location, key string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return s.manager().Verify(ctx, id, loc, key)
}

func (s *storageSvc) createRepo(ctx context.Context, loc kopiarepo.Location, key string) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return kopiarepo.Create(ctx, loc, key)
}

func (s *storageSvc) find(ctx context.Context, id int64) (*storage_entity.Storage, error) {
	st, err := storage_repo.Storage().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, i18n.NewNotFoundError(ctx, code.StorageNotFound)
	}
	return st, nil
}

// savedLocation 用已保存的参数（解密 Secret Key）还原位置
func (s *storageSvc) savedLocation(ctx context.Context, st *storage_entity.Storage) (kopiarepo.Location, error) {
	secretKey := ""
	if st.SecretKey != "" {
		var err error
		if secretKey, err = secret_svc.Secret().Decrypt(ctx, st.SecretKey); err != nil {
			return kopiarepo.Location{}, err
		}
	}
	return st.Location(secretKey), nil
}

// save 保存存储；它在测试或解锁期间已被删除时返回“不存在”，并清理这次操作在本机留下的连接配置
func (s *storageSvc) save(ctx context.Context, st *storage_entity.Storage) error {
	err := storage_repo.Storage().Save(ctx, st)
	if !errors.Is(err, storage_repo.ErrNotFound) {
		return err
	}
	if rmErr := s.manager().Remove(st.ID); rmErr != nil {
		logger.Ctx(ctx).Warn("清理存储的本机缓存失败", zap.Int64("storage_id", st.ID), zap.Error(rmErr))
	}
	return i18n.NewNotFoundError(ctx, code.StorageNotFound)
}

// apply 写入位置、Secret Key 与仓库密钥（repoKey 为空表示不变），并标记为测试通过
func (s *storageSvc) apply(ctx context.Context, st *storage_entity.Storage, name string, loc kopiarepo.Location, repoKey string) error {
	st.Name = name
	st.SetLocation(loc)
	st.SecretKey = ""
	if loc.Kind == kopiarepo.KindS3 {
		ct, err := secret_svc.Secret().Encrypt(ctx, loc.SecretKey)
		if err != nil {
			return err
		}
		st.SecretKey = ct
	}
	if repoKey != "" {
		ct, err := secret_svc.Secret().Encrypt(ctx, repoKey)
		if err != nil {
			return err
		}
		st.RepoKey = ct
		st.Fingerprint = kopiarepo.Fingerprint(repoKey)
	}
	now := s.now().Unix()
	st.Status, st.StatusCode, st.StatusDetail = storage_entity.StatusOK, 0, ""
	st.Checktime, st.Updatetime = now, now
	return nil
}

func (s *storageSvc) List(ctx context.Context, _ *api.ListRequest) (*api.ListResponse, error) {
	rows, err := storage_repo.Storage().List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*api.Item, 0, len(rows))
	for _, st := range rows {
		items = append(items, s.toItem(ctx, st))
	}
	return &api.ListResponse{Items: items}, nil
}

func (s *storageSvc) Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error) {
	var existing *storage_entity.Storage
	if req.ID != 0 {
		var err error
		if existing, err = s.find(ctx, req.ID); err != nil {
			return nil, err
		}
	}
	_, loc, err := s.location(ctx, req.Name, req.Location, existing)
	if err != nil {
		return nil, err
	}
	res, err := s.probe(ctx, loc)
	if err != nil {
		return nil, err
	}
	resp := &api.ProbeResponse{
		State:           string(res.State),
		Location:        loc.String(),
		LocationChanged: existing != nil && existing.LocationKey != loc.Key(),
	}
	if !res.CreatedAt.IsZero() {
		resp.CreatedAt = res.CreatedAt.Unix()
	}
	return resp, nil
}

func (s *storageSvc) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	name, loc, err := s.location(ctx, req.Name, req.Location, nil)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(req.Key)
	res, err := s.probe(ctx, loc)
	if err != nil {
		return nil, err
	}
	snapshots := 0
	switch res.State {
	case kopiarepo.StateNotEmpty:
		return nil, i18n.NewError(ctx, code.StorageLocationNotEmpty)
	case kopiarepo.StateEmpty:
		if err := checkNewKey(ctx, key, req.ConfirmSaved); err != nil {
			return nil, err
		}
		if err := s.createRepo(ctx, loc, key); err != nil {
			return nil, repoError(ctx, err)
		}
	case kopiarepo.StateRepository:
		if key == "" {
			return nil, i18n.NewError(ctx, code.StorageKeyRequired)
		}
		if snapshots, err = s.verify(ctx, 0, loc, key); err != nil {
			return nil, repoError(ctx, err)
		}
	}

	st := &storage_entity.Storage{Createtime: s.now().Unix()}
	if err := s.apply(ctx, st, name, loc, key); err != nil {
		return nil, err
	}
	if err := storage_repo.Storage().Create(ctx, st); err != nil {
		return nil, err
	}
	return &api.CreateResponse{Item: s.toItem(ctx, st), Snapshots: snapshots}, nil
}

// checkNewKey 在空位置建库前校验密钥与确认勾选
func checkNewKey(ctx context.Context, key string, confirmed bool) error {
	switch {
	case key == "":
		return i18n.NewError(ctx, code.StorageKeyRequired)
	case utf8.RuneCountInString(key) < minKeyLength:
		return i18n.NewError(ctx, code.StorageKeyTooShort)
	case !confirmed:
		return i18n.NewError(ctx, code.StorageKeyNotConfirmed)
	}
	return nil
}

func (s *storageSvc) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	st, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	name, loc, err := s.location(ctx, req.Name, req.Location, st)
	if err != nil {
		return nil, err
	}
	changed := loc.Key() != st.LocationKey
	if changed && !req.ConfirmLocationChange {
		return nil, i18n.NewError(ctx, code.StorageLocationChangeConfirm)
	}
	managedKey, err := secret_svc.Secret().Decrypt(ctx, st.RepoKey)
	if err != nil {
		return nil, err
	}
	res, err := s.probe(ctx, loc)
	if err != nil {
		return nil, err
	}

	snapshots, newKey := 0, ""
	switch {
	case !changed:
		// 位置没变：托管密钥必须仍能打开同一个仓库
		if res.State != kopiarepo.StateRepository {
			return nil, i18n.NewError(ctx, code.StorageNotRepository)
		}
		snapshots, err = s.verify(ctx, st.ID, loc, managedKey)
		if errors.Is(err, kopiarepo.ErrInvalidPassword) {
			return nil, i18n.NewError(ctx, code.StorageManagedKeyInvalid)
		}
		if err != nil {
			return nil, repoError(ctx, err)
		}
	case res.State == kopiarepo.StateNotEmpty:
		return nil, i18n.NewError(ctx, code.StorageLocationNotEmpty)
	case res.State == kopiarepo.StateEmpty:
		// 新位置为空：沿用托管密钥建库
		if err := s.createRepo(ctx, loc, managedKey); err != nil {
			return nil, repoError(ctx, err)
		}
	default:
		// 新位置已是仓库：用提交的密钥解锁，成功后改用它
		newKey = strings.TrimSpace(req.Key)
		if newKey == "" {
			return nil, i18n.NewError(ctx, code.StorageKeyRequired)
		}
		if snapshots, err = s.verify(ctx, st.ID, loc, newKey); err != nil {
			return nil, repoError(ctx, err)
		}
	}

	if err := s.apply(ctx, st, name, loc, newKey); err != nil {
		return nil, err
	}
	if err := s.save(ctx, st); err != nil {
		return nil, err
	}
	return &api.UpdateResponse{Item: s.toItem(ctx, st), Snapshots: snapshots}, nil
}

func (s *storageSvc) Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error) {
	st, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	loc, err := s.savedLocation(ctx, st)
	if err != nil {
		return nil, err
	}
	managedKey, err := secret_svc.Secret().Decrypt(ctx, st.RepoKey)
	if err != nil {
		return nil, err
	}

	snapshots, status, statusCode, detail := 0, storage_entity.StatusOK, 0, ""
	probeCtx, cancel := context.WithTimeout(ctx, opTimeout)
	res, err := kopiarepo.Probe(probeCtx, loc)
	cancel()
	switch {
	case err != nil:
		status = storage_entity.StatusUnreachable
		statusCode, detail = classify(err)
	case res.State != kopiarepo.StateRepository:
		status, statusCode = storage_entity.StatusUnreachable, code.StorageNotRepository
	default:
		snapshots, err = s.verify(ctx, st.ID, loc, managedKey)
		switch {
		case errors.Is(err, kopiarepo.ErrInvalidPassword):
			status, statusCode = storage_entity.StatusWrongKey, code.StorageManagedKeyInvalid
		case err != nil:
			status = storage_entity.StatusUnreachable
			statusCode, detail = classify(err)
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	if status != storage_entity.StatusOK {
		logger.Ctx(ctx).Info("存储测试未通过", zap.Int64("storage_id", st.ID), zap.String("status", status), zap.Int("code", statusCode))
	}

	st.Status, st.StatusCode, st.StatusDetail = status, statusCode, detail
	st.Checktime = s.now().Unix()
	if err := s.save(ctx, st); err != nil {
		return nil, err
	}
	return &api.TestResponse{Item: s.toItem(ctx, st), Snapshots: snapshots}, nil
}

func (s *storageSvc) Unlock(ctx context.Context, req *api.UnlockRequest) (*api.UnlockResponse, error) {
	st, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return nil, i18n.NewError(ctx, code.StorageKeyRequired)
	}
	loc, err := s.savedLocation(ctx, st)
	if err != nil {
		return nil, err
	}
	res, err := s.probe(ctx, loc)
	if err != nil {
		return nil, err
	}
	if res.State != kopiarepo.StateRepository {
		return nil, i18n.NewError(ctx, code.StorageNotRepository)
	}
	snapshots, err := s.verify(ctx, st.ID, loc, key)
	if err != nil {
		return nil, repoError(ctx, err)
	}
	if err := s.apply(ctx, st, st.Name, loc, key); err != nil {
		return nil, err
	}
	if err := s.save(ctx, st); err != nil {
		return nil, err
	}
	return &api.UnlockResponse{Item: s.toItem(ctx, st), Snapshots: snapshots}, nil
}

func (s *storageSvc) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	st, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := storage_repo.Storage().Delete(ctx, st.ID); err != nil {
		return nil, err
	}
	// 只清理本机的 kopia 配置与缓存，存储中的数据保留
	if err := s.manager().Remove(st.ID); err != nil {
		logger.Ctx(ctx).Warn("清理存储的本机缓存失败", zap.Int64("storage_id", st.ID), zap.Error(err))
	}
	return &api.DeleteResponse{}, nil
}

func (s *storageSvc) Key(_ context.Context, req *api.KeyRequest) (*api.KeyResponse, error) {
	key := strings.TrimSpace(req.Key)
	if key == "" {
		var err error
		if key, err = kopiarepo.GenerateKey(); err != nil {
			return nil, err
		}
	}
	return &api.KeyResponse{Key: key, Fingerprint: kopiarepo.Fingerprint(key), Encryption: kopiarepo.Encryption}, nil
}

func (s *storageSvc) Reveal(ctx context.Context, req *api.RevealRequest, meta auth_svc.ClientMeta) (*api.RevealResponse, error) {
	p := authctx.From(ctx)
	if p == nil || p.Via != authctx.ViaSession {
		return nil, i18n.NewForbiddenError(ctx, code.SessionRequired)
	}
	st, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	enabled, err := auth_svc.Auth().PasswordLoginEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if enabled {
		if err := auth_svc.Auth().VerifyPassword(ctx, req.Password, meta); err != nil {
			return nil, err
		}
	} else if !auth_svc.Auth().ConsumeReauth(p.SessionID) {
		return nil, i18n.NewForbiddenError(ctx, code.StorageReauthRequired)
	}
	key, err := secret_svc.Secret().Decrypt(ctx, st.RepoKey)
	if err != nil {
		return nil, err
	}
	logger.Ctx(ctx).Info("查看了存储的仓库密钥", zap.Int64("storage_id", st.ID), zap.Int64("session_id", p.SessionID))
	return &api.RevealResponse{Key: key, Fingerprint: st.Fingerprint}, nil
}

func (s *storageSvc) ListDirs(ctx context.Context, req *api.ListDirsRequest) (*api.ListDirsResponse, error) {
	path := strings.TrimSpace(req.Path)
	if fi, err := os.Stat(path); path == "" || !filepath.IsAbs(path) || err != nil || !fi.IsDir() {
		s.mu.RLock()
		path = s.browseStart
		s.mu.RUnlock()
	}
	path = filepath.Clean(path)
	dirs, err := kopiarepo.ListDirs(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, i18n.NewError(ctx, code.StorageDirNoAccess)
		}
		return nil, err
	}
	resp := &api.ListDirsResponse{Path: path, Dirs: make([]*api.Dir, 0, len(dirs))}
	if parent := filepath.Dir(path); parent != path {
		resp.Parent = parent
	}
	for _, d := range dirs {
		resp.Dirs = append(resp.Dirs, &api.Dir{Name: d.Name, Path: d.Path, Status: string(d.Status)})
	}
	return resp, nil
}

func (s *storageSvc) MakeDir(ctx context.Context, req *api.MakeDirRequest) (*api.MakeDirResponse, error) {
	path, err := kopiarepo.Mkdir(strings.TrimSpace(req.Parent), req.Name)
	switch {
	case err == nil:
		return &api.MakeDirResponse{Path: path}, nil
	case errors.Is(err, kopiarepo.ErrRelativePath):
		return nil, i18n.NewError(ctx, code.StoragePathRelative)
	case errors.Is(err, kopiarepo.ErrInvalidDirName):
		return nil, i18n.NewError(ctx, code.StorageDirNameInvalid)
	case errors.Is(err, fs.ErrExist):
		return nil, i18n.NewError(ctx, code.StorageDirExists)
	case errors.Is(err, fs.ErrPermission):
		return nil, i18n.NewError(ctx, code.StorageDirNoPermission)
	}
	return nil, i18n.NewError(ctx, code.StorageDirCreateFailed, err.Error())
}
