// Package masterkey_svc 主密钥：启动时核对、轮换（用新主密钥重新加密全部凭据）
package masterkey_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	settingapi "github.com/opskat/opsnap/internal/api/setting"
	"github.com/opskat/opsnap/internal/model/entity/masterkey_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/masterkey_repo"
)

// ErrMismatch 当前主密钥解不开保存的校验值：OPSNAP_SECRET_KEY 不是加密现有凭据的那把主密钥
var ErrMismatch = errors.New("主密钥不匹配：OPSNAP_SECRET_KEY 解不开已保存的凭据。如果刚轮换过主密钥，请把环境变量改成新主密钥后再启动")

// MasterKeySvc 主密钥服务
type MasterKeySvc interface {
	// Verify 启动时核对主密钥：没有校验值时写入一份；解不开时返回 ErrMismatch，调用方应拒绝启动
	Verify(ctx context.Context) error
	// Rotate 轮换主密钥，见 settingapi.RotateMasterKeyRequest
	Rotate(ctx context.Context, req *settingapi.RotateMasterKeyRequest) (*settingapi.RotateMasterKeyResponse, error)
	// RotatedAt 最近一次轮换的时间，没有轮换过为 0
	RotatedAt(ctx context.Context) (int64, error)
}

type masterKeySvc struct {
	now func() time.Time
	// mu 同一时间只做一次轮换
	mu sync.Mutex
}

func newMasterKey(now func() time.Time) *masterKeySvc { return &masterKeySvc{now: now} }

var defaultMasterKey MasterKeySvc = newMasterKey(time.Now)

// MasterKey 取得主密钥服务
func MasterKey() MasterKeySvc { return defaultMasterKey }

// RegisterMasterKey 替换服务实现
func RegisterMasterKey(i MasterKeySvc) { defaultMasterKey = i }

// checkWith 用 sec 解开校验值，内容一致才算同一把主密钥
func checkWith(sec secret.Secret, m *masterkey_entity.MasterKey) bool {
	p, err := sec.Decrypt(m.CheckValue)
	return err == nil && p == masterkey_entity.CheckPlain
}

func (s *masterKeySvc) Verify(ctx context.Context) error {
	sec := secret.Default()
	if sec == nil {
		return secret.ErrMissingKey
	}
	m, err := masterkey_repo.MasterKey().Get(ctx)
	if err != nil {
		return err
	}
	if m != nil {
		if !checkWith(sec, m) {
			return ErrMismatch
		}
		return nil
	}
	check, err := sec.Encrypt(masterkey_entity.CheckPlain)
	if err != nil {
		return err
	}
	return masterkey_repo.MasterKey().Save(ctx, &masterkey_entity.MasterKey{CheckValue: check, Createtime: s.now().Unix()})
}

func (s *masterKeySvc) Rotate(ctx context.Context, req *settingapi.RotateMasterKeyRequest) (*settingapi.RotateMasterKeyResponse, error) {
	if strings.TrimSpace(req.NewKey) == "" {
		return nil, i18n.NewError(ctx, code.MasterKeyEmpty)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 等进行中的凭据保存落库，并在换上新主密钥之前挡住新的保存
	endRotate := secret.BeginRotate()
	defer endRotate()
	old := secret.Default()
	if old == nil {
		return nil, secret.ErrMissingKey
	}
	next, err := secret.New(req.NewKey)
	if err != nil {
		return nil, i18n.NewError(ctx, code.MasterKeyEmpty)
	}
	m, err := masterkey_repo.MasterKey().Get(ctx)
	if err != nil {
		return nil, err
	}
	if m != nil && checkWith(next, m) {
		return nil, i18n.NewError(ctx, code.MasterKeySame)
	}
	check, err := next.Encrypt(masterkey_entity.CheckPlain)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	saved := &masterkey_entity.MasterKey{CheckValue: check, RotatedAt: now, Createtime: now}
	if m != nil {
		saved.Createtime = m.Createtime
	}
	n := 0
	err = masterkey_repo.MasterKey().Rekey(ctx, func(cipher string) (string, error) {
		n++
		plain, err := old.Decrypt(cipher)
		if err != nil {
			return "", fmt.Errorf("第 %d 项凭据用当前主密钥解不开", n)
		}
		return next.Encrypt(plain)
	}, saved)
	if err != nil {
		return nil, i18n.NewError(ctx, code.MasterKeyRotateFailed, err.Error())
	}
	secret.SetDefault(next)
	return &settingapi.RotateMasterKeyResponse{RotatedAt: now}, nil
}

func (s *masterKeySvc) RotatedAt(ctx context.Context) (int64, error) {
	m, err := masterkey_repo.MasterKey().Get(ctx)
	if err != nil || m == nil {
		return 0, err
	}
	return m.RotatedAt, nil
}
