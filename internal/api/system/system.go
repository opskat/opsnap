// Package system 定义系统级接口的请求与响应。
package system

import "github.com/cago-frame/cago/server/mux"

// 元数据库状态
const (
	DatabaseOK    = "ok"
	DatabaseError = "error"
)

// HealthRequest 健康检查：返回版本号与元数据库状态，供前端和部署探活使用
type HealthRequest struct {
	mux.Meta `path:"/system/health" method:"GET"`
}

type HealthResponse struct {
	Version  string `json:"version"`
	Database string `json:"database"`
}
