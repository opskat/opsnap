// Package repolock 按存储位置的共享/独占锁：备份写入取共享锁，可以并行；
// 维护（清理、校验、解锁、更换密钥）取独占锁，与写入它的备份互斥。
// 已有独占请求在排队时，新的共享请求排在它后面，避免维护被源源不断的备份饿死。
// 锁只在本进程内有效，restic 自身的仓库锁仍然生效。
package repolock

import (
	"context"
	"sync"
)

// Mode 锁的模式
type Mode int

const (
	// Shared 共享锁：备份写入
	Shared Mode = iota
	// Exclusive 独占锁：维护
	Exclusive
)

// state 一个存储位置的锁状态
type state struct {
	readers        int
	writer         bool
	waitingWriters int
}

// Locks 一组按存储位置 ID 区分的读写锁
type Locks struct {
	mu     sync.Mutex
	repos  map[int64]*state
	change chan struct{} // 状态变化时关闭并替换，唤醒所有等待者
}

// New 创建一组锁
func New() *Locks {
	return &Locks{repos: map[int64]*state{}, change: make(chan struct{})}
}

// broadcast 唤醒所有等待者；调用方持有 mu
func (l *Locks) broadcast() {
	close(l.change)
	l.change = make(chan struct{})
}

func (l *Locks) get(id int64) *state {
	s, ok := l.repos[id]
	if !ok {
		s = &state{}
		l.repos[id] = s
	}
	return s
}

// tryLocked 尝试加锁；queued 表示调用方已登记为排队的独占请求。调用方持有 mu
func (l *Locks) tryLocked(id int64, mode Mode, queued bool) bool {
	s := l.get(id)
	switch mode {
	case Exclusive:
		if s.writer || s.readers > 0 {
			return false
		}
		if queued {
			s.waitingWriters--
		}
		s.writer = true
	default:
		if s.writer || s.waitingWriters > 0 {
			return false
		}
		s.readers++
	}
	return true
}

// release 返回只生效一次的释放函数
func (l *Locks) release(id int64, mode Mode) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			s := l.get(id)
			if mode == Exclusive {
				s.writer = false
			} else {
				s.readers--
			}
			if !s.writer && s.readers == 0 && s.waitingWriters == 0 {
				delete(l.repos, id)
			}
			l.broadcast()
		})
	}
}

// Acquire 等待并取得存储位置 repoID 的锁，返回只生效一次的释放函数。
// ctx 结束时放弃等待，返回 context.Cause(ctx)。
func (l *Locks) Acquire(ctx context.Context, repoID int64, mode Mode) (func(), error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	l.mu.Lock()
	queued := false
	for {
		if l.tryLocked(repoID, mode, queued) {
			l.mu.Unlock()
			return l.release(repoID, mode), nil
		}
		if mode == Exclusive && !queued {
			l.get(repoID).waitingWriters++
			queued = true
		}
		wait := l.change
		l.mu.Unlock()
		select {
		case <-wait:
			l.mu.Lock()
		case <-ctx.Done():
			l.mu.Lock()
			if queued {
				s := l.get(repoID)
				s.waitingWriters--
				if !s.writer && s.readers == 0 && s.waitingWriters == 0 {
					delete(l.repos, repoID)
				}
				// 撤下排队的独占请求后，被它挡住的共享请求可以继续
				l.broadcast()
			}
			l.mu.Unlock()
			return nil, context.Cause(ctx)
		}
	}
}

// TryAcquire 不等待地尝试取得锁；拿不到时返回 false
func (l *Locks) TryAcquire(repoID int64, mode Mode) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.tryLocked(repoID, mode, false) {
		return nil, false
	}
	return l.release(repoID, mode), true
}

var defaultLocks = New()

// Acquire 用进程内默认的锁集合取得存储位置的锁，见 (*Locks).Acquire
func Acquire(ctx context.Context, repoID int64, mode Mode) (func(), error) {
	return defaultLocks.Acquire(ctx, repoID, mode)
}

// TryAcquire 用进程内默认的锁集合不等待地尝试取得锁，见 (*Locks).TryAcquire
func TryAcquire(repoID int64, mode Mode) (func(), bool) {
	return defaultLocks.TryAcquire(repoID, mode)
}
