package snapshot_ctr

import (
	"io"
	"net/http"
	"time"
)

// downloadIdleTimeout 客户端多久没有读走数据就中止下载。下载期间持有存储位置的共享锁，
// 被暂停的下载如果一直挂着，排队的维护会挡住之后的每一次备份。
const downloadIdleTimeout = 5 * time.Minute

// deadlineWriter 每次写入前把连接的写超时推后 idle：持续有进展的下载不受总时长限制，
// 停止读取的客户端在 idle 之后写入失败
type deadlineWriter struct {
	w    io.Writer
	rc   *http.ResponseController
	idle time.Duration
}

func idleWriter(w http.ResponseWriter, idle time.Duration) io.Writer {
	return &deadlineWriter{w: w, rc: http.NewResponseController(w), idle: idle}
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	// 不支持写超时的 ResponseWriter（如测试里的 ResponseRecorder）返回 ErrNotSupported，照常写入
	_ = d.rc.SetWriteDeadline(time.Now().Add(d.idle))
	return d.w.Write(p)
}
