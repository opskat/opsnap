package repo_svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
)

// existProbeTimeout 保存前探测仓库是否存在的时限。正常情况下 restic 几秒内就返回 10 或 12；
// 访问不了时它会把多数后端错误当成可重试错误重试 15 分钟，不能等它自己结束。
const existProbeTimeout = time.Minute

var (
	// errBucketMissing restic 的输出表明 S3 bucket 不存在
	errBucketMissing = errors.New("S3 bucket 不存在")
	// errProbeTimeout 探测到时限仍没有结果
	errProbeTimeout = errors.New("探测仓库超时")
)

// probeRepo 用新生成的 opsnap 密钥探测位置，返回这里是否已经是 restic 仓库：
// 仓库不存在返回 10，已有仓库但密码不对返回 12（restic ≥ 0.17）；S3 bucket 不存在也算空位置，
// restic init 会建 bucket。到时限仍没有结果时返回说明原因的错误，带上 restic 最后一次重试的错误。
func (s *repoSvc) probeRepo(ctx context.Context, ent *repo_entity.Repo, sess *session, opsnapKey string) (bool, error) {
	pctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pctx, cancelTimeout := context.WithTimeoutCause(pctx, s.probeLimit, errProbeTimeout)
	defer cancelTimeout()
	var watch *bucketWatch
	var tee io.Writer
	if ent.Type == repo_entity.TypeS3 {
		watch = &bucketWatch{onMissing: func() { cancel(errBucketMissing) }}
		tee = watch
	}
	res := s.execTee(pctx, tee)(cmdbuild.ResticSnapshots(sess.repo(opsnapKey), nil))
	switch {
	// 认出 bucket 不存在后，restic 可能被终止，也可能在终止前自己退出了：两种都按空位置处理
	case watch != nil && watch.seen():
		return false, nil
	case res.err == nil && res.code == exitNoRepository:
		return false, nil
	case res.err == nil && res.code == exitWrongPassword:
		return true, nil
	case errors.Is(res.err, errProbeTimeout):
		return false, i18n.NewError(ctx, code.RepoProbeTimeout, fmtLimit(s.probeLimit), res.lastRetryError())
	}
	return false, i18n.NewError(ctx, code.RepoAccessFailed, res.reason())
}

// bucketWatch 在 restic 的输出里认出「bucket 不存在」，认出后调用 onMissing 一次。
// restic 经 minio-go 访问 S3，读配置文件遇到 NoSuchBucket 时不当作「仓库不存在」，
// 而是每次重试都在标准错误打印「… returned error, retrying after …: The specified bucket does not exist」，
// 一直重试到 15 分钟。看到第一条就可以判定位置为空，不必等下去。
type bucketWatch struct {
	onMissing func()

	mu    sync.Mutex
	tail  string // 上一次写入的末尾，防止关键字被拆在两次写入之间
	fired bool
}

// bucketWatchTail 保留的末尾长度，比关键字长即可
const bucketWatchTail = 256

func (w *bucketWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fired {
		return len(p), nil
	}
	text := w.tail + string(p)
	if bucketMissing(text) {
		w.fired = true
		w.onMissing()
		return len(p), nil
	}
	if len(text) > bucketWatchTail {
		text = text[len(text)-bucketWatchTail:]
	}
	w.tail = text
	return len(p), nil
}

// seen 是否已在输出里认出 bucket 不存在
func (w *bucketWatch) seen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}

// bucketMissing 输出里是否有 S3 的 NoSuchBucket：错误码本身，或 S3 与 minio-go 对它的标准描述
func bucketMissing(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "nosuchbucket") || strings.Contains(lower, "the specified bucket does not exist")
}

// resticRetryMarker restic 每次重试打印的「<操作> returned error, retrying after <间隔>: <错误>」中间的部分
const resticRetryMarker = " returned error, retrying after "

// lastRetryError restic 最后一次重试打印的错误本身（已遮盖凭据）。restic 被取消后最后一行是 exit_error，
// 只说 context canceled，看不出原因；没有重试记录时退回最后一条输出。
func (r result) lastRetryError() string {
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		at := strings.Index(lines[i], resticRetryMarker)
		if at < 0 {
			continue
		}
		rest := lines[i][at+len(resticRetryMarker):]
		if sep := strings.Index(rest, ": "); sep >= 0 {
			if msg := strings.TrimSpace(rest[sep+2:]); msg != "" {
				return clipTail(msg)
			}
		}
	}
	return r.lastLine()
}

// lastLine restic 输出的最后一条非空行（已遮盖凭据），没有输出时说明没有输出
func (r result) lastLine() string {
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return "restic 没有输出"
	}
	return clipTail(last)
}

// fmtLimit 把时限写成「1 分钟」「30 秒」
func fmtLimit(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", d/time.Minute)
	}
	return fmt.Sprintf("%d 秒", int(math.Ceil(d.Seconds())))
}
