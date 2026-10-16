package repo_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/repolock"
)

func keysOf(r *repo_entity.Repo) repoapi.Keys {
	return repoapi.Keys{
		OpsnapKey: repoapi.KeyStatus{
			Present: r.OpsnapKey != "", ID: r.OpsnapKeyID, CreatedAt: r.OpsnapKeyCreatedAt,
		},
		RecoveryKey: repoapi.KeyStatus{
			Present: r.HasRecoveryKey(), ID: r.RecoveryKeyID, CreatedAt: r.RecoveryKeyCreatedAt,
		},
		NoRecoveryKey: !r.HasRecoveryKey(),
	}
}

// keyOp 密钥操作的准备：独占存储位置、重新读取 r（加锁前读到的可能已被另一次密钥操作改过）、
// 挂载检查、打开后端、解密 opsnap 密钥。返回的 done 释放全部资源。
func (s *repoSvc) keyOp(ctx context.Context, r *repo_entity.Repo) (sess *session, opsnapKey string, done func(), err error) {
	release, ok := repolock.TryAcquire(r.ID, repolock.Exclusive)
	if !ok {
		return nil, "", nil, i18n.NewError(ctx, code.RepoBusy)
	}
	fail := func(err error) (*session, string, func(), error) {
		release()
		return nil, "", nil, err
	}
	fresh, err := s.find(ctx, r.ID)
	if err != nil {
		return fail(err)
	}
	*r = *fresh
	if err := s.preflightErr(ctx, r); err != nil {
		return fail(err)
	}
	if opsnapKey, err = decrypt(r.OpsnapKey); err != nil {
		return fail(err)
	}
	if sess, err = s.open(ctx, r); err != nil {
		return fail(err)
	}
	return sess, opsnapKey, func() { sess.close(); release() }, nil
}
