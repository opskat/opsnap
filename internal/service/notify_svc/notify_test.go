package notify_svc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	notifyapi "github.com/opskat/opsnap/internal/api/notify"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/migrations"
)

// hitRecorder 记录 Webhook 收到的请求；handler 在 httptest 的 goroutine 里执行，所以加锁
type hitRecorder struct {
	mu      sync.Mutex
	count   int
	headers http.Header
	body    map[string]any
}

func (h *hitRecorder) record(r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.headers = r.Header.Clone()
	h.body = body
}

func (h *hitRecorder) hits() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// newWebhook 启动一个 Webhook 替身，每次请求按 status 返回；status 为函数时可按次数变化
func newWebhook(t *testing.T, rec *hitRecorder, status func(n int) int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.WriteHeader(status(rec.hits()))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fixture struct {
	ctx    context.Context
	svc    NotifySvc
	sleeps []time.Duration
}

func setupNotifyTest(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	s, err := secret.New("notify-test-master-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	notify_repo.RegisterNotify(notify_repo.NewNotify())
	f := &fixture{ctx: db.WithContextDB(context.Background(), gdb)}
	f.svc = NewNotify(func() time.Time { return time.Unix(1_800_000_000, 0) }, func(d time.Duration) {
		f.sleeps = append(f.sleeps, d)
	})
	return f
}

// save 整体保存配置，事件默认值与 spec 一致：失败、恢复、太久没有成功开启，每次成功关闭
func (f *fixture) save(t *testing.T, url string, headers map[string]string) {
	t.Helper()
	_, err := f.svc.Update(f.ctx, &notifyapi.UpdateRequest{
		URL: url, Headers: headers, OnFailed: true, OnRecovered: true, OnStale: true, OnSuccess: false, StaleHours: 48,
	})
	require.NoError(t, err)
}

func sampleEvent(typ notify_entity.EventType) Event {
	return Event{
		Type:          typ,
		JobID:         7,
		JobName:       "每日数据库",
		RunID:         42,
		StartedAt:     time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC),
		Duration:      90 * time.Second,
		ErrorSummary:  "mysqldump 退出码 2",
		LastSuccessAt: time.Date(2026, 10, 14, 2, 0, 0, 0, time.UTC),
		Link:          "https://nas.example/runs/42",
	}
}

func TestNotifySend(t *testing.T) {
	convey := func(name string, fn func(t *testing.T, f *fixture)) {
		t.Run(name, func(t *testing.T) { fn(t, setupNotifyTest(t)) })
	}

	convey("按配置的地址和请求头以 JSON POST 发送，并携带事件字段", func(t *testing.T, f *fixture) {
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusOK })
		f.save(t, srv.URL+"/hook", map[string]string{"Authorization": "Bearer abc", "X-Env": "prod"})

		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed)))

		assert.Equal(t, 1, rec.hits())
		assert.Equal(t, "application/json", rec.headers.Get("Content-Type"))
		assert.Equal(t, "Bearer abc", rec.headers.Get("Authorization"))
		assert.Equal(t, "prod", rec.headers.Get("X-Env"))
		assert.Equal(t, "failed", rec.body["type"])
		assert.Equal(t, float64(7), rec.body["job_id"])
		assert.Equal(t, "每日数据库", rec.body["job_name"])
		assert.Equal(t, float64(42), rec.body["run_id"])
		assert.Equal(t, "2026-10-15T02:00:00Z", rec.body["started_at"])
		assert.Equal(t, float64(90), rec.body["duration_seconds"])
		assert.Equal(t, "mysqldump 退出码 2", rec.body["error_summary"])
		assert.Equal(t, "2026-10-14T02:00:00Z", rec.body["last_success_at"])
		assert.Equal(t, "https://nas.example/runs/42", rec.body["link"])
	})

	convey("请求头值以密文保存，读取时只返回占位", func(t *testing.T, f *fixture) {
		f.save(t, "https://hooks.example/x", map[string]string{"Authorization": "Bearer abc"})

		stored, err := notify_repo.Notify().Get(f.ctx)
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.NotEqual(t, "Bearer abc", stored.Headers["Authorization"])

		view, err := f.svc.View(f.ctx)
		require.NoError(t, err)
		assert.Equal(t, secret.Masked, view.Headers["Authorization"])
	})

	convey("停用的事件和未配置地址时不发送", func(t *testing.T, f *fixture) {
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusOK })
		f.save(t, srv.URL, nil)

		// 每次成功默认关闭
		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventSuccess)))
		assert.Equal(t, 0, rec.hits())

		// 开启后才发送
		_, err := f.svc.Update(f.ctx, &notifyapi.UpdateRequest{URL: srv.URL, OnFailed: true, OnRecovered: true, OnStale: true, OnSuccess: true, StaleHours: 48})
		require.NoError(t, err)
		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventSuccess)))
		assert.Equal(t, 1, rec.hits())

		// 关闭备份失败事件后不发送
		_, err = f.svc.Update(f.ctx, &notifyapi.UpdateRequest{URL: srv.URL, OnRecovered: true, OnStale: true, StaleHours: 48})
		require.NoError(t, err)
		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed)))
		assert.Equal(t, 1, rec.hits())

		// 地址为空时任何事件都不发送，也不报错
		f.save(t, "", nil)
		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed)))
		assert.Equal(t, 1, rec.hits())
	})

	convey("5xx 和连接失败重试 3 次，指数退避 1s、2s、4s，最后返回错误", func(t *testing.T, f *fixture) {
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusInternalServerError })
		f.save(t, srv.URL, nil)

		err := f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed))
		require.Error(t, err)
		assert.Equal(t, 4, rec.hits(), "首次发送加 3 次重试")
		assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, f.sleeps)
	})

	convey("失败后第二次成功即返回成功", func(t *testing.T, f *fixture) {
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(n int) int {
			if n <= 2 {
				return http.StatusServiceUnavailable
			}
			return http.StatusNoContent
		})
		f.save(t, srv.URL, nil)

		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventRecovered)))
		assert.Equal(t, 3, rec.hits())
		assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, f.sleeps)
	})

	convey("连接失败同样重试后返回错误", func(t *testing.T, f *fixture) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		addr := srv.URL
		srv.Close()
		f.save(t, addr, nil)

		require.Error(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventStale)))
		assert.Len(t, f.sleeps, 3)
	})

	convey("链接是面板路径时补上保存的面板地址，成为能直接打开的完整链接", func(t *testing.T, f *fixture) {
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusOK })
		_, err := f.svc.Update(f.ctx, &notifyapi.UpdateRequest{
			URL: srv.URL, OnFailed: true, StaleHours: 48, PanelURL: "http://opsnap.lan:8080/",
		})
		require.NoError(t, err)
		view, err := f.svc.View(f.ctx)
		require.NoError(t, err)
		assert.Equal(t, "http://opsnap.lan:8080", view.PanelURL)

		ev := sampleEvent(notify_entity.EventFailed)
		ev.Link = "/runs/42"
		require.NoError(t, f.svc.Send(f.ctx, ev))
		rec.mu.Lock()
		defer rec.mu.Unlock()
		assert.Equal(t, "http://opsnap.lan:8080/runs/42", rec.body["link"])
	})

	convey("4xx 同样重试 3 次后放弃", func(t *testing.T, f *fixture) {
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusBadRequest })
		f.save(t, srv.URL, nil)

		err := f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "400")
		assert.Equal(t, 4, rec.hits(), "首次发送加 3 次重试")
		assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, f.sleeps)
	})
}

func TestNotifyTest(t *testing.T) {
	t.Run("测试消息返回对方的状态码，且只发一次", func(t *testing.T) {
		f := setupNotifyTest(t)
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusTeapot })
		f.save(t, srv.URL, map[string]string{"Authorization": "Bearer abc"})

		resp, err := f.svc.Test(f.ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusTeapot, resp.StatusCode)
		assert.Equal(t, 1, rec.hits())
		assert.Equal(t, "Bearer abc", rec.headers.Get("Authorization"))
		assert.Equal(t, "test", rec.body["type"])
		assert.Empty(t, f.sleeps)
	})

	t.Run("连接失败时返回错误", func(t *testing.T) {
		f := setupNotifyTest(t)
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		addr := srv.URL
		srv.Close()
		f.save(t, addr, nil)

		_, err := f.svc.Test(f.ctx)
		require.Error(t, err)
	})

	t.Run("未配置地址时返回 NotifyURLEmpty", func(t *testing.T) {
		f := setupNotifyTest(t)
		_, err := f.svc.Test(f.ctx)
		var he *httputils.Error
		require.ErrorAs(t, err, &he)
		assert.Equal(t, code.NotifyURLEmpty, he.Code)
	})
}

func TestNotifySettings(t *testing.T) {
	t.Run("默认值符合 spec，阈值可保存", func(t *testing.T) {
		f := setupNotifyTest(t)
		view, err := f.svc.View(f.ctx)
		require.NoError(t, err)
		assert.Empty(t, view.URL)
		assert.True(t, view.OnFailed)
		assert.True(t, view.OnRecovered)
		assert.True(t, view.OnStale)
		assert.False(t, view.OnSuccess)
		assert.Equal(t, 48, view.StaleHours)

		_, err = f.svc.Update(f.ctx, &notifyapi.UpdateRequest{URL: "https://h.example", OnFailed: true, StaleHours: 72})
		require.NoError(t, err)
		view, err = f.svc.View(f.ctx)
		require.NoError(t, err)
		assert.Equal(t, 72, view.StaleHours)
	})

	t.Run("提交占位值保留已保存的请求头，未提交的请求头被删除", func(t *testing.T) {
		f := setupNotifyTest(t)
		rec := &hitRecorder{}
		srv := newWebhook(t, rec, func(int) int { return http.StatusOK })
		f.save(t, srv.URL, map[string]string{"Authorization": "Bearer abc", "X-Env": "prod"})

		f.save(t, srv.URL, map[string]string{"Authorization": secret.Masked})
		require.NoError(t, f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed)))
		assert.Equal(t, "Bearer abc", rec.headers.Get("Authorization"))
		assert.Empty(t, rec.headers.Get("X-Env"))
	})

	t.Run("非法输入返回对应错误码", func(t *testing.T) {
		f := setupNotifyTest(t)
		cases := map[string]struct {
			req  *notifyapi.UpdateRequest
			want int
		}{
			"地址不是 http(s)":   {&notifyapi.UpdateRequest{URL: "ftp://x.example", StaleHours: 48}, code.NotifyURLInvalid},
			"阈值为 0":          {&notifyapi.UpdateRequest{URL: "https://x.example", StaleHours: 0}, code.NotifyStaleHoursInvalid},
			"请求头名含空格":        {&notifyapi.UpdateRequest{URL: "https://x.example", StaleHours: 48, Headers: map[string]string{"Bad Name": "v"}}, code.NotifyHeaderInvalid},
			"请求头值含换行":        {&notifyapi.UpdateRequest{URL: "https://x.example", StaleHours: 48, Headers: map[string]string{"X-A": "a\r\nb"}}, code.NotifyHeaderInvalid},
			"面板地址不是 http(s)": {&notifyapi.UpdateRequest{StaleHours: 48, PanelURL: "javascript:alert(1)"}, code.NotifyPanelURLInvalid},
			"占位值但没有已保存值":     {&notifyapi.UpdateRequest{URL: "https://x.example", StaleHours: 48, Headers: map[string]string{"X-A": secret.Masked}}, code.NotifyHeaderValueMissing},
		}
		for name, c := range cases {
			_, err := f.svc.Update(f.ctx, c.req)
			var he *httputils.Error
			require.ErrorAs(t, err, &he, name)
			assert.Equal(t, c.want, he.Code, name)
		}
	})
}

func TestNotifyDoesNotFollowRedirects(t *testing.T) {
	f := setupNotifyTest(t)
	elsewhere := &hitRecorder{}
	other := newWebhook(t, elsewhere, func(int) int { return http.StatusOK })
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/collect", http.StatusFound)
	}))
	t.Cleanup(hook.Close)
	f.save(t, hook.URL+"/hook", map[string]string{"X-Token": "SECRET-token"})

	err := f.svc.Send(f.ctx, sampleEvent(notify_entity.EventFailed))
	require.Error(t, err, "重定向不算送达：302 后变成没有正文的 GET")
	assert.Contains(t, err.Error(), "302")
	assert.Zero(t, elsewhere.hits(), "请求头里的令牌不能被带到重定向的地址")
}
