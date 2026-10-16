package run_svc

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/redact"
)

// liveLog 一次运行的日志：每一行在写入前遮盖已知凭据，实时推送与落库用的都是遮盖后的内容。
type liveLog struct {
	red *redact.Redactor
	now func() time.Time

	mu      sync.Mutex
	lines   []run_entity.LogLine
	changed chan struct{} // 有新行或结束时关闭并替换
	done    bool
	status  string
	reason  string
}

func newLiveLog(now func() time.Time) *liveLog {
	return &liveLog{red: redact.New(), now: now, changed: make(chan struct{})}
}

// secret 加入需要遮盖的凭据
func (l *liveLog) secret(s ...string) { l.red.Add(s...) }

// redact 遮盖文本里的凭据（失败原因、步骤说明也要经过它）
func (l *liveLog) redact(s string) string { return l.red.String(s) }

// add 追加日志；多行文本按行拆开，空行忽略
func (l *liveLog) add(level, text string) {
	text = l.red.String(text)
	t := l.now().UnixMilli()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	added := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		l.lines = append(l.lines, run_entity.LogLine{Time: t, Level: level, Text: line})
		added = true
	}
	if added {
		l.broadcast()
	}
}

func (l *liveLog) infof(format string, args ...any) {
	l.add(run_entity.LevelInfo, fmt.Sprintf(format, args...))
}
func (l *liveLog) warnf(format string, args ...any) {
	l.add(run_entity.LevelWarn, fmt.Sprintf(format, args...))
}
func (l *liveLog) errorf(format string, args ...any) {
	l.add(run_entity.LevelError, fmt.Sprintf(format, args...))
}

// broadcast 需持有 mu
func (l *liveLog) broadcast() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// snapshot 当前全部日志的副本
func (l *liveLog) snapshot() []run_entity.LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]run_entity.LogLine{}, l.lines...)
}

// since 返回 cursor 之后的行、下一次变化的通知、是否已结束及结果
func (l *liveLog) since(cursor int) (lines []run_entity.LogLine, changed <-chan struct{}, done bool, status, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cursor < len(l.lines) {
		lines = append(lines, l.lines[cursor:]...)
	}
	return lines, l.changed, l.done, l.status, l.reason
}

// close 运行结束：之后不再追加，订阅者收到结束
func (l *liveLog) close(status, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	l.done, l.status, l.reason = true, status, reason
	l.broadcast()
}

// maxLine 一行的长度上限，超过时先按已收到的部分记一行，避免没有换行的输出占满内存
const maxLine = 64 << 10

// lineWriter 把子进程输出切成行交给 fn；可并发写入
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(line string)
	// crIsNewline 把 \r 也当作换行：rsync 的进度行用 \r 原地刷新，需要实时切出来
	crIsNewline bool
}

func newLineWriter(fn func(line string)) *lineWriter { return &lineWriter{fn: fn} }

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.crIsNewline {
		p = bytes.ReplaceAll(p, []byte{'\r'}, []byte{'\n'})
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.fn(strings.TrimRight(line, "\r"))
	}
	if len(w.buf) > maxLine {
		w.fn(string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}

// Flush 交出最后一段没有换行的输出
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}

// textLevel 按内容判断普通文本输出（rsync、导出工具的错误输出）的级别
func textLevel(line string) string {
	l := strings.ToLower(line)
	for _, k := range []string{"error", "fatal", "failed", "denied", "错误", "失败"} {
		if strings.Contains(l, k) {
			return run_entity.LevelError
		}
	}
	for _, k := range []string{"warning", "vanished", "警告"} {
		if strings.Contains(l, k) {
			return run_entity.LevelWarn
		}
	}
	return run_entity.LevelInfo
}

// textWriter 普通文本输出写进日志
func (l *liveLog) textWriter() *lineWriter {
	return newLineWriter(func(line string) { l.add(textLevel(line), line) })
}
