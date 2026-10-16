// Package notify_svc 通知（Webhook）：设置读写、测试消息与事件发送
package notify_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	notifyapi "github.com/opskat/opsnap/internal/api/notify"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
)

const (
	// maxRetries 首次发送失败后的重试次数，共 1 + 3 次尝试
	maxRetries = 3
	// baseBackoff 指数退避的初始等待，之后每次翻倍：1s、2s、4s
	baseBackoff = time.Second
	// maxDrainBytes 读取响应体的上限，只为复用连接，不解析内容
	maxDrainBytes = 4096
	// typeTest 测试消息的事件类型
	typeTest = "test"
)

// httpClient 发送 Webhook 的客户端，超时后按失败处理并重试。
// 不跟随重定向：跟随时 POST 会变成没有正文的 GET，自定义请求头里的令牌也会被带到新地址；3xx 按失败处理。
var httpClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Event 一次需要通知的事件。Link 是面板中对应页面的路径（如 /runs/42），发送时按通知设置里的面板地址拼成完整链接。
type Event struct {
	Type          notify_entity.EventType
	JobID         int64
	JobName       string
	RunID         int64
	StartedAt     time.Time
	Duration      time.Duration
	ErrorSummary  string
	LastSuccessAt time.Time
	Link          string
}

// NotifySvc 通知服务
type NotifySvc interface {
	// View 返回通知设置；请求头值只返回占位
	View(ctx context.Context) (*notifyapi.GetResponse, error)
	// Update 整体保存通知设置
	Update(ctx context.Context, req *notifyapi.UpdateRequest) (*notifyapi.UpdateResponse, error)
	// Test 发送一条测试消息，不重试，返回对方的状态码
	Test(ctx context.Context) (*notifyapi.TestResponse, error)
	// Send 按通知设置发送事件，供运行引擎调用
	Send(ctx context.Context, e Event) error
}

type notifySvc struct {
	now   func() time.Time
	sleep func(time.Duration)
}

// NewNotify 创建通知服务；sleep 注入退避等待，测试时可替换成记录
func NewNotify(now func() time.Time, sleep func(time.Duration)) NotifySvc {
	return &notifySvc{now: now, sleep: sleep}
}

var defaultNotify NotifySvc = NewNotify(time.Now, time.Sleep)

// Notify 取得通知服务
func Notify() NotifySvc { return defaultNotify }

// RegisterNotify 替换服务实现
func RegisterNotify(i NotifySvc) { defaultNotify = i }

// Send 按通知设置发送事件的快捷方式，等同 Notify().Send(ctx, e)。
// 停用的事件、未配置地址时直接返回 nil。发送失败时重试 3 次后返回错误，
// 调用方据此在运行记录里注明「通知发送失败」。
func Send(ctx context.Context, e Event) error { return defaultNotify.Send(ctx, e) }

func (s *notifySvc) load(ctx context.Context) (*notify_entity.Notify, error) {
	got, err := notify_repo.Notify().Get(ctx)
	if err != nil {
		return nil, err
	}
	if got == nil {
		return notify_entity.Default(), nil
	}
	return got, nil
}

func (s *notifySvc) View(ctx context.Context) (*notifyapi.GetResponse, error) {
	cfg, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	masked := make(map[string]string, len(cfg.Headers))
	for name := range cfg.Headers {
		masked[name] = secret.Masked
	}
	return &notifyapi.GetResponse{
		URL:         cfg.URL,
		Headers:     masked,
		OnFailed:    cfg.OnFailed,
		OnRecovered: cfg.OnRecovered,
		OnStale:     cfg.OnStale,
		OnSuccess:   cfg.OnSuccess,
		StaleHours:  cfg.StaleHours,
		PanelURL:    cfg.PanelURL,
	}, nil
}

func (s *notifySvc) Update(ctx context.Context, req *notifyapi.UpdateRequest) (*notifyapi.UpdateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	old, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(req.Headers))
	for name, value := range req.Headers {
		if !validHeaderName(name) || strings.ContainsAny(value, "\r\n") {
			return nil, i18n.NewError(ctx, code.NotifyHeaderInvalid)
		}
		if value == secret.Masked {
			// 占位值表示保留已保存的密文，不重新加密
			saved, ok := old.Headers[name]
			if !ok {
				return nil, i18n.NewError(ctx, code.NotifyHeaderValueMissing, name)
			}
			headers[name] = saved
			continue
		}
		enc, err := encrypt(value)
		if err != nil {
			return nil, err
		}
		headers[name] = enc
	}
	cfg := &notify_entity.Notify{
		URL:         req.URL,
		Headers:     headers,
		OnFailed:    req.OnFailed,
		OnRecovered: req.OnRecovered,
		OnStale:     req.OnStale,
		OnSuccess:   req.OnSuccess,
		StaleHours:  req.StaleHours,
		PanelURL:    strings.TrimRight(strings.TrimSpace(req.PanelURL), "/"),
		Updatetime:  s.now().Unix(),
	}
	if err := cfg.Validate(ctx); err != nil {
		return nil, err
	}
	if err := notify_repo.Notify().Save(ctx, cfg); err != nil {
		return nil, err
	}
	view, err := s.View(ctx)
	if err != nil {
		return nil, err
	}
	return &notifyapi.UpdateResponse{GetResponse: *view}, nil
}

func (s *notifySvc) Test(ctx context.Context) (*notifyapi.TestResponse, error) {
	cfg, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.URL == "" {
		return nil, i18n.NewError(ctx, code.NotifyURLEmpty)
	}
	headers, err := decrypt(cfg.Headers)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload{Type: typeTest})
	if err != nil {
		return nil, err
	}
	status, err := post(ctx, cfg.URL, headers, body)
	if err != nil {
		return nil, i18n.NewError(ctx, code.NotifyTestFailed, err.Error())
	}
	return &notifyapi.TestResponse{StatusCode: status}, nil
}

func (s *notifySvc) Send(ctx context.Context, e Event) error {
	cfg, err := s.load(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled(e.Type) {
		return nil
	}
	headers, err := decrypt(cfg.Headers)
	if err != nil {
		return err
	}
	e.Link = cfg.LinkTo(e.Link)
	body, err := json.Marshal(newPayload(e))
	if err != nil {
		return err
	}
	return s.deliver(ctx, cfg.URL, headers, body)
}

// deliver 发送一条事件：任何失败（连接错误或非 2xx）都重试，共重试 3 次后放弃。
// 首次尝试与每次重试之间按 1s、2s、4s 退避。
func (s *notifySvc) deliver(ctx context.Context, target string, headers map[string]string, body []byte) error {
	var lastErr error
	wait := baseBackoff
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			s.sleep(wait)
			wait *= 2
		}
		status, err := post(ctx, target, headers, body)
		switch {
		case err == nil && status/100 == 2:
			return nil
		case err != nil:
			lastErr = err
		default:
			lastErr = fmt.Errorf("对方返回状态码 %d", status)
		}
	}
	return fmt.Errorf("重试 %d 次后仍失败: %w", maxRetries, lastErr)
}

// post 以 JSON POST 发送一次，返回对方的状态码。
// 连接错误中的 URL 会被去掉，避免地址里的令牌出现在错误信息中。
func post(ctx context.Context, target string, headers map[string]string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	return resp.StatusCode, nil
}

// payload Webhook 的 JSON 消息体。字段始终出现，未知时为空串或 0，接收方不需要判断字段是否存在。
type payload struct {
	Type            string `json:"type"`
	JobID           int64  `json:"job_id"`
	JobName         string `json:"job_name"`
	RunID           int64  `json:"run_id"`
	StartedAt       string `json:"started_at"`
	DurationSeconds int64  `json:"duration_seconds"`
	ErrorSummary    string `json:"error_summary"`
	LastSuccessAt   string `json:"last_success_at"`
	Link            string `json:"link"`
}

func newPayload(e Event) payload {
	return payload{
		Type:            string(e.Type),
		JobID:           e.JobID,
		JobName:         e.JobName,
		RunID:           e.RunID,
		StartedAt:       formatTime(e.StartedAt),
		DurationSeconds: int64(e.Duration / time.Second),
		ErrorSummary:    e.ErrorSummary,
		LastSuccessAt:   formatTime(e.LastSuccessAt),
		Link:            e.Link,
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// validHeaderName 请求头名不能为空，也不能含空白和冒号
func validHeaderName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t\r\n:")
}

// encrypt 用主密钥加密请求头值
func encrypt(value string) (string, error) {
	s := secret.Default()
	if s == nil {
		return "", errors.New("主密钥未加载，无法加密请求头")
	}
	return s.Encrypt(value)
}

// decrypt 还原已保存的请求头
func decrypt(saved map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(saved))
	if len(saved) == 0 {
		return out, nil
	}
	s := secret.Default()
	if s == nil {
		return nil, errors.New("主密钥未加载，无法解密请求头")
	}
	for name, cipher := range saved {
		plain, err := s.Decrypt(cipher)
		if err != nil {
			return nil, err
		}
		out[name] = plain
	}
	return out, nil
}
