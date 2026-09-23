// Package web 把前端构建产物内嵌进二进制，并以单页应用方式提供。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/cago-frame/cago/configs"
	"github.com/gin-gonic/gin"
)

// dist 由 frontend 构建写入（pnpm -C frontend build）；仓库里只保留 .gitkeep 占位
//
//go:embed all:dist
var dist embed.FS

// Register 作为 mux 中间件注册：未匹配到接口的 GET 请求返回静态文件，找不到时回退到 index.html
func Register(_ *configs.Config, r *gin.Engine) error {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return err
	}
	r.NoRoute(Handler(sub))
	return nil
}

// Handler 返回单页应用处理器；/api/ 下的未知路径保持 404，避免把接口拼写错误伪装成页面
func Handler(files fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(files))
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if c.Request.Method != http.MethodGet || strings.HasPrefix(path, "/api/") {
			c.Status(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(path, "/")
		if name != "" {
			if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			c.String(http.StatusNotFound, "前端尚未构建：请先执行 make build")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	}
}
