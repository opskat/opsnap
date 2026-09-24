// Package secret_svc 管理主密钥的加载与校验，并对外提供加解密。
package secret_svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
)

// checkSettingKey 保存一段用主密钥加密的固定文本，启动时解密它来确认主密钥没有换
const (
	checkSettingKey = "master_key_check"
	checkPlaintext  = "opsnap-master-key-check"
)

// 主密钥来源
const (
	SourceEnv  = "env"
	SourceFile = "file"
)

var (
	ErrKeyMissing     = errors.New("主密钥缺失")
	ErrKeyMismatch    = errors.New("主密钥与数据库不匹配")
	ErrNotInitialized = errors.New("主密钥尚未初始化")
)

type InitOptions struct {
	// DataDir 数据目录，master.key 位于其中
	DataDir string
	// EnvKey 环境变量 OPSNAP_MASTER_KEY 的值；非空时优先使用
	EnvKey string
}

type InitResult struct {
	Source    string
	Path      string
	Generated bool
}

type SecretSvc interface {
	// Init 加载或生成主密钥，并与数据库中的校验值比对；必须在使用加解密前、迁移之后调用
	Init(ctx context.Context, opts InitOptions) (*InitResult, error)
	Encrypt(ctx context.Context, plaintext string) (string, error)
	Decrypt(ctx context.Context, ciphertext string) (string, error)
}

type secretSvc struct {
	mu  sync.RWMutex
	box *secret.Box
}

var defaultSecret SecretSvc = newSecret()

func Secret() SecretSvc {
	return defaultSecret
}

func newSecret() *secretSvc {
	return &secretSvc{}
}

func (s *secretSvc) Init(ctx context.Context, opts InitOptions) (*InitResult, error) {
	check, hasCheck, err := setting_repo.Setting().Get(ctx, checkSettingKey)
	if err != nil {
		return nil, fmt.Errorf("读取主密钥校验值: %w", err)
	}

	res := &InitResult{}
	var key []byte
	var where string
	if opts.EnvKey != "" {
		res.Source, where = SourceEnv, "环境变量 "+secret.EnvKey
		if key, err = secret.ParseKey(opts.EnvKey); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
	} else {
		res.Source, res.Path = SourceFile, filepath.Join(opts.DataDir, secret.KeyFileName)
		where = res.Path
		key, err = secret.ReadKeyFile(res.Path)
		switch {
		case errors.Is(err, os.ErrNotExist) && hasCheck:
			// 数据库里已有用旧密钥加密的数据：生成新密钥只会让它们永久无法解密
			return nil, fmt.Errorf("%w：数据库中已有加密数据，但未找到 %s，也未设置环境变量 %s；请恢复原来的密钥文件或设置该环境变量",
				ErrKeyMissing, res.Path, secret.EnvKey)
		case errors.Is(err, os.ErrNotExist):
			if key, err = secret.GenerateKey(); err != nil {
				return nil, err
			}
			if err := secret.WriteKeyFile(res.Path, key); err != nil {
				return nil, fmt.Errorf("写入主密钥文件 %s: %w", res.Path, err)
			}
			res.Generated = true
		case err != nil:
			return nil, fmt.Errorf("读取主密钥文件: %w", err)
		}
	}

	box, err := secret.NewBox(key)
	if err != nil {
		return nil, err
	}
	if hasCheck {
		if pt, err := box.Decrypt(check); err != nil || string(pt) != checkPlaintext {
			return nil, fmt.Errorf("%w：请检查 %s 是否为加密这些数据时使用的密钥", ErrKeyMismatch, where)
		}
	} else {
		ct, err := box.Encrypt([]byte(checkPlaintext))
		if err != nil {
			return nil, err
		}
		if err := setting_repo.Setting().Set(ctx, checkSettingKey, ct); err != nil {
			return nil, fmt.Errorf("保存主密钥校验值: %w", err)
		}
	}

	s.mu.Lock()
	s.box = box
	s.mu.Unlock()
	return res, nil
}

func (s *secretSvc) current() (*secret.Box, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.box == nil {
		return nil, ErrNotInitialized
	}
	return s.box, nil
}

func (s *secretSvc) Encrypt(_ context.Context, plaintext string) (string, error) {
	box, err := s.current()
	if err != nil {
		return "", err
	}
	return box.Encrypt([]byte(plaintext))
}

func (s *secretSvc) Decrypt(_ context.Context, ciphertext string) (string, error) {
	box, err := s.current()
	if err != nil {
		return "", err
	}
	pt, err := box.Decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
