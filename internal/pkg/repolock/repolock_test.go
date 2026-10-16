package repolock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acquired 在 d 内 ch 收到值
func acquired(ch <-chan func(), d time.Duration) (func(), bool) {
	select {
	case rel := <-ch:
		return rel, true
	case <-time.After(d):
		return nil, false
	}
}

// acquireAsync 在后台请求锁，拿到后把释放函数发到返回的通道
func acquireAsync(t *testing.T, l *Locks, ctx context.Context, id int64, mode Mode) (<-chan func(), <-chan error) {
	t.Helper()
	ok := make(chan func(), 1)
	fail := make(chan error, 1)
	go func() {
		rel, err := l.Acquire(ctx, id, mode)
		if err != nil {
			fail <- err
			return
		}
		ok <- rel
	}()
	return ok, fail
}

func TestSharedLocksCoexist(t *testing.T) {
	l := New()
	ctx := context.Background()
	r1, err := l.Acquire(ctx, 1, Shared)
	require.NoError(t, err)
	r2, err := l.Acquire(ctx, 1, Shared)
	require.NoError(t, err, "两个备份可以同时写同一个存储位置")
	r1()
	r2()
}

func TestExclusiveWaitsForShared(t *testing.T) {
	l := New()
	ctx := context.Background()
	shared, err := l.Acquire(ctx, 1, Shared)
	require.NoError(t, err)

	ex, _ := acquireAsync(t, l, ctx, 1, Exclusive)
	_, got := acquired(ex, 50*time.Millisecond)
	assert.False(t, got, "有备份在写时维护必须等待")

	// 其他存储位置不受影响
	other, err := l.Acquire(ctx, 2, Exclusive)
	require.NoError(t, err)
	other()

	shared()
	rel, got := acquired(ex, time.Second)
	require.True(t, got, "备份结束后维护拿到独占锁")

	// 维护期间新的备份要等待
	sh, _ := acquireAsync(t, l, ctx, 1, Shared)
	_, got = acquired(sh, 50*time.Millisecond)
	assert.False(t, got, "维护期间备份必须等待")
	rel()
	relShared, got := acquired(sh, time.Second)
	require.True(t, got)
	relShared()
}

func TestWaitingExclusiveBlocksNewShared(t *testing.T) {
	l := New()
	ctx := context.Background()
	first, err := l.Acquire(ctx, 1, Shared)
	require.NoError(t, err)
	ex, _ := acquireAsync(t, l, ctx, 1, Exclusive)
	time.Sleep(20 * time.Millisecond) // 让独占请求进入等待

	sh, _ := acquireAsync(t, l, ctx, 1, Shared)
	_, got := acquired(sh, 50*time.Millisecond)
	assert.False(t, got, "已有维护在排队时新的备份不能插队，避免维护永远等不到")

	first()
	relEx, got := acquired(ex, time.Second)
	require.True(t, got)
	relEx()
	relSh, got := acquired(sh, time.Second)
	require.True(t, got)
	relSh()
}

func TestAcquireHonorsCancel(t *testing.T) {
	l := New()
	held, err := l.Acquire(context.Background(), 1, Exclusive)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	_, fail := acquireAsync(t, l, ctx, 1, Exclusive)
	sh, shFail := acquireAsync(t, l, context.Background(), 1, Shared)
	cancel()
	select {
	case err := <-fail:
		assert.True(t, errors.Is(err, context.Canceled))
	case <-time.After(time.Second):
		t.Fatal("取消后 Acquire 应立即返回")
	}

	held()
	// 被取消的独占请求不能残留，否则共享锁会一直等下去
	rel, got := acquired(sh, time.Second)
	require.True(t, got)
	assert.Empty(t, shFail)
	rel()

	// 已取消的 ctx 直接返回错误
	_, err = l.Acquire(ctx, 1, Shared)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReleaseIsIdempotentAndTry(t *testing.T) {
	l := New()
	rel, ok := l.TryAcquire(1, Exclusive)
	require.True(t, ok)
	_, ok = l.TryAcquire(1, Shared)
	assert.False(t, ok, "独占期间 TryAcquire 立即失败")
	rel()
	rel() // 重复释放不能把别人的锁也放掉

	sh, ok := l.TryAcquire(1, Shared)
	require.True(t, ok)
	_, ok = l.TryAcquire(1, Exclusive)
	assert.False(t, ok)
	sh()
	ex, ok := l.TryAcquire(1, Exclusive)
	require.True(t, ok)
	ex()
}

func TestDefault(t *testing.T) {
	rel, err := Acquire(context.Background(), 99, Shared)
	require.NoError(t, err)
	rel()
	rel2, ok := TryAcquire(99, Exclusive)
	require.True(t, ok)
	rel2()
}
