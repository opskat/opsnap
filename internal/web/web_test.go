package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func serve(files fstest.MapFS, method, path string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(Handler(files))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestHandler(t *testing.T) {
	files := fstest.MapFS{
		"index.html":       {Data: []byte("<html>app</html>")},
		"assets/app.js":    {Data: []byte("console.log(1)")},
		"assets/empty/.gk": {Data: []byte("")},
	}
	convey.Convey("单页应用托管", t, func() {
		convey.Convey("存在的静态文件直接返回", func() {
			w := serve(files, http.MethodGet, "/assets/app.js")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "console.log(1)", w.Body.String())
		})
		convey.Convey("前端路由回退到 index.html", func() {
			w := serve(files, http.MethodGet, "/jobs/new")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "<html>app</html>", w.Body.String())
		})
		convey.Convey("目录不当作文件返回，回退到 index.html", func() {
			w := serve(files, http.MethodGet, "/assets/empty")
			assert.Equal(t, "<html>app</html>", w.Body.String())
		})
		convey.Convey("/api/ 下的未知路径保持 404，不伪装成页面", func() {
			w := serve(files, http.MethodGet, "/api/v1/nope")
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.NotContains(t, w.Body.String(), "<html>")
		})
		convey.Convey("非 GET 请求不回退", func() {
			w := serve(files, http.MethodPost, "/jobs/new")
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
		convey.Convey("前端未构建时给出提示", func() {
			w := serve(fstest.MapFS{}, http.MethodGet, "/")
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "make build")
		})
	})
}
