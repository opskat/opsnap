// Package notify 通知（Webhook）接口
package notify

import "github.com/cago-frame/cago/server/mux"

// GetRequest 读取通知设置
type GetRequest struct {
	mux.Meta `path:"/notify" method:"GET"`
}

// GetResponse 通知设置；请求头值不返回明文，只返回占位
type GetResponse struct {
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	OnFailed    bool              `json:"on_failed"`
	OnRecovered bool              `json:"on_recovered"`
	OnStale     bool              `json:"on_stale"`
	OnSuccess   bool              `json:"on_success"`
	StaleHours  int               `json:"stale_hours"`
	// PanelURL 面板地址，消息里的 link 以它开头；面板保存时自动填入浏览器里的地址
	PanelURL string `json:"panel_url"`
}

// UpdateRequest 保存通知设置，整体提交。
// Headers 的值为占位「已设置」时保留已保存的值，其余值会被加密保存；未出现的请求头会被删除。
type UpdateRequest struct {
	mux.Meta    `path:"/notify" method:"PUT"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	OnFailed    bool              `json:"on_failed"`
	OnRecovered bool              `json:"on_recovered"`
	OnStale     bool              `json:"on_stale"`
	OnSuccess   bool              `json:"on_success"`
	StaleHours  int               `json:"stale_hours"`
	// PanelURL 面板地址（如 http://opsnap.lan:8080），为空时 link 只有路径
	PanelURL string `json:"panel_url"`
}

type UpdateResponse struct {
	GetResponse
}

// TestRequest 发送一条测试消息
type TestRequest struct {
	mux.Meta `path:"/notify/test" method:"POST"`
}

// TestResponse 对方返回的 HTTP 状态码
type TestResponse struct {
	StatusCode int `json:"status_code"`
}
