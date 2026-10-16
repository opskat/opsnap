package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler 返回单页应用的处理函数：存在的静态文件直接返回，
// 其余路径（包括根路径和前端路由）都返回 index.html，由前端路由接管。
func Handler(fsys fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(fsys))
	return func(c *gin.Context) {
		name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		// index.html 走下面的统一出口，避免 http.FileServer 把它重定向到目录
		if name != "" && name != "index.html" && fs.ValidPath(name) {
			if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			c.String(http.StatusNotFound, "index.html not found")
			return
		}
		// 入口页不缓存，前端更新后浏览器能立即拿到新的资源引用
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	}
}
