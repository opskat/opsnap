package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serve 用给定文件系统构造处理函数并处理一次请求
func serve(t *testing.T, fsys fs.FS, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(Handler(fsys))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":         {Data: []byte("<html>index</html>")},
		"assets/app-abc.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc.css": {Data: []byte("body{}")},
	}

	convey.Convey("内嵌面板处理函数", t, func() {
		convey.Convey("存在的静态文件按原样返回", func() {
			w := serve(t, fsys, http.MethodGet, "/assets/app-abc.js")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "console.log(1)", w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "javascript")
		})

		convey.Convey("根路径、目录和不存在的路径都返回 index.html", func() {
			for _, p := range []string{"/", "/index.html", "/assets", "/assets/", "/jobs/1", "/../etc/passwd"} {
				w := serve(t, fsys, http.MethodGet, p)
				assert.Equal(t, http.StatusOK, w.Code, p)
				assert.Contains(t, w.Header().Get("Content-Type"), "text/html", p)
				assert.Equal(t, "<html>index</html>", w.Body.String(), p)
			}
		})

		convey.Convey("缺少 index.html 时返回 404", func() {
			w := serve(t, fstest.MapFS{}, http.MethodGet, "/jobs")
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	})
}

func TestDist(t *testing.T) {
	convey.Convey("内嵌产物", t, func() {
		convey.Convey("总能读到 index.html", func() {
			b, err := fs.ReadFile(Dist(), "index.html")
			require.NoError(t, err)
			assert.NotEmpty(t, b)
		})

		built := fstest.MapFS{"index.html": {Data: []byte("built")}}
		fallback := fstest.MapFS{"index.html": {Data: []byte("placeholder")}}

		convey.Convey("前端已构建时使用构建产物", func() {
			b, err := fs.ReadFile(pick(built, fallback), "index.html")
			require.NoError(t, err)
			assert.Equal(t, "built", string(b))
		})

		convey.Convey("前端未构建（只有占位文件）时使用占位页", func() {
			empty := fstest.MapFS{".keep": {Data: nil}}
			b, err := fs.ReadFile(pick(empty, fallback), "index.html")
			require.NoError(t, err)
			assert.Equal(t, "placeholder", string(b))
		})
	})
}
