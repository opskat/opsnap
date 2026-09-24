package middleware

import (
	"io"
	"regexp"
)

// sensitiveQuery 访问日志中需要隐去取值的查询参数：OIDC 回调的授权码与 state
var sensitiveQuery = regexp.MustCompile(`([?&](?:code|state)=)[^&\s"]*`)

type redactWriter struct {
	w io.Writer
}

func (r redactWriter) Write(p []byte) (int, error) {
	if _, err := r.w.Write(sensitiveQuery.ReplaceAll(p, []byte("${1}[已隐去]"))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// RedactAccessLog 包装访问日志的输出：调试模式下 gin 会把完整请求地址（含查询参数）写入日志，
// 这里隐去 OIDC 授权码等敏感参数的取值。需要在 HTTP 组件创建 gin 日志中间件之前设置为 gin.DefaultWriter
func RedactAccessLog(w io.Writer) io.Writer {
	return redactWriter{w: w}
}
