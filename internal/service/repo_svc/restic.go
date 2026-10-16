package repo_svc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/redact"
)

// restic ≥ 0.17 的退出码
const (
	exitNoRepository  = 10 // 仓库不存在
	exitWrongPassword = 12 // 密码不对
)

// CheckSubset 校验数据时抽样读取的比例
const CheckSubset = "5%"

// maxReason 错误原因最多保留的输出长度（取末尾）
const maxReason = 400

// result 一次命令执行的结果
type result struct {
	code int
	// out 标准输出与标准错误合并的内容，用于展示原因
	out string
	// stdout 只有标准输出，解析 JSON 等结构化输出时用它，避免混入标准错误上的提示
	stdout string
	err    error
}

func (r result) ok() bool { return r.err == nil && r.code == 0 }

// reason 失败原因：启动错误，或输出的最后几行，或退出码
func (r result) reason() string {
	if r.err != nil {
		return r.err.Error()
	}
	out := strings.TrimSpace(r.out)
	if out == "" {
		return fmt.Sprintf("退出码 %d", r.code)
	}
	return clipTail(out)
}

// clipTail 超过 maxReason 个字符时只保留末尾
func clipTail(s string) string {
	if rs := []rune(s); len(rs) > maxReason {
		return "…" + string(rs[len(rs)-maxReason:])
	}
	return s
}

// exec 返回执行器：直接接收 cmdbuild 构建函数的 (Cmd, error)，执行命令并收集输出，
// 写法为 s.exec(ctx)(cmdbuild.ResticInit(r))
func (s *repoSvc) exec(ctx context.Context) func(cmdbuild.Cmd, error) result {
	return s.execTee(ctx, nil)
}

// execTee 同 exec，另把未遮盖的合并输出实时写给 tee（可为 nil），用于运行中识别输出；
// tee 只能在进程内判断，不能把内容写进日志或错误信息
func (s *repoSvc) execTee(ctx context.Context, tee io.Writer) func(cmdbuild.Cmd, error) result {
	return func(c cmdbuild.Cmd, buildErr error) result {
		if buildErr != nil {
			return result{code: -1, err: buildErr}
		}
		all := &lockedBuffer{}
		var sink io.Writer = all
		if tee != nil {
			sink = io.MultiWriter(all, tee)
		}
		var code int
		var err error
		var stdout bytes.Buffer
		if st, ok := s.runner.(executor.Streamer); ok {
			code, err = st.RunSplit(ctx, c, io.MultiWriter(&stdout, sink), sink)
		} else {
			code, err = s.runner.Run(ctx, c, io.MultiWriter(&stdout, sink))
		}
		// out 会写进存储位置的状态和维护记录，遮盖命令环境里的凭据；stdout 只用于解析，不遮盖
		red := redact.New(c.Secrets()...)
		return result{code: code, out: red.String(all.String()), stdout: stdout.String(), err: err}
	}
}

// lockedBuffer 标准输出和标准错误由不同的 goroutine 写入，合并时加锁
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// keyInfo restic key list --json 的一项
type keyInfo struct {
	Current bool   `json:"current"`
	ID      string `json:"id"`
}

// listKeys 用 r 的密码打开仓库并列出密钥；输出无法解析时视为失败
func (s *repoSvc) listKeys(ctx context.Context, r cmdbuild.Repo) ([]keyInfo, result) {
	res := s.exec(ctx)(cmdbuild.ResticKeyList(r))
	if !res.ok() {
		return nil, res
	}
	var keys []keyInfo
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.stdout)), &keys); err != nil {
		return nil, result{code: -1, err: fmt.Errorf("无法解析 restic key list 的输出：%w", err)}
	}
	return keys, res
}

func currentKey(keys []keyInfo) string {
	for _, k := range keys {
		if k.Current {
			return k.ID
		}
	}
	return ""
}

// addKey 用当前密码 cur 打开仓库加入 newKey，再用 newKey 打开仓库验证，返回新密钥的 ID。
// 加入后验证失败时撤掉新加入的密钥，仓库保持原状。返回的错误说明失败在哪一步。
func (s *repoSvc) addKey(ctx context.Context, sess *session, cur, newKey string) (string, error) {
	before, res := s.listKeys(ctx, sess.repo(cur))
	if !res.ok() {
		return "", fmt.Errorf("读取现有密钥：%s", res.reason())
	}
	if res = s.exec(ctx)(cmdbuild.ResticKeyAdd(sess.repo(cur), newKey)); !res.ok() {
		return "", fmt.Errorf("添加新密钥：%s", res.reason())
	}
	keys, res := s.listKeys(ctx, sess.repo(newKey))
	id := currentKey(keys)
	if !res.ok() || id == "" {
		s.removeAdded(ctx, sess, cur, before)
		return "", fmt.Errorf("验证新密钥：%s", res.reason())
	}
	return id, nil
}

// removeAdded 撤掉 before 之后新加入的密钥（尽力而为）。调用方取消时也要撤，所以不随 ctx 取消。
func (s *repoSvc) removeAdded(ctx context.Context, sess *session, cur string, before []keyInfo) {
	ctx = context.WithoutCancel(ctx)
	after, res := s.listKeys(ctx, sess.repo(cur))
	if !res.ok() {
		return
	}
	known := make(map[string]bool, len(before))
	for _, k := range before {
		known[k.ID] = true
	}
	for _, k := range after {
		if !known[k.ID] && !k.Current {
			s.removeKey(ctx, sess.repo(cur), k.ID)
		}
	}
}

// removeKey 删除一把密钥（用于回滚，尽力而为）
func (s *repoSvc) removeKey(ctx context.Context, r cmdbuild.Repo, id string) result {
	return s.exec(context.WithoutCancel(ctx))(cmdbuild.ResticKeyRemove(r, id))
}

// newOpsnapKey 生成 opsnap 密钥：32 字节随机数
func newOpsnapKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newRecoveryKey 生成恢复密钥：25 字节（200 位）随机数，base32 后每 5 个字符一组，方便抄写
func newRecoveryKey() (string, error) {
	b := make([]byte, 25)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	groups := make([]string, 0, len(s)/5)
	for i := 0; i < len(s); i += 5 {
		groups = append(groups, s[i:i+5])
	}
	return strings.Join(groups, "-"), nil
}

// emptyDir 目录是否为空（忽略标记文件，它可能是上一次失败的初始化留下的）
func emptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() != repo_entity.MarkerFile {
			return false, nil
		}
	}
	return true, nil
}

// markerContent 标记文件的内容
const markerContent = "这个目录是 opsnap 的 restic 仓库。opsnap 每次运行前检查本文件，找不到就判定 NAS 没有挂载，不会写入。请勿删除。\n"

// writeMarker 写入标记文件；已存在时不动，返回是否新写入
func writeMarker(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // 路径来自管理员配置的仓库目录
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("写入标记文件 %s 失败：%w", path, err)
	}
	if _, err := f.WriteString(markerContent); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return false, fmt.Errorf("写入标记文件 %s 失败：%w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("写入标记文件 %s 失败：%w", path, err)
	}
	return true, nil
}
