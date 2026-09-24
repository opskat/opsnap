package middleware

import "github.com/gin-gonic/gin"

// ConfigureEngine 设置全局中间件与客户端 IP 的取法：只有来自 trustedProxies 的请求才采用 X-Forwarded-For，
// 否则一律取连接对端地址（Gin 默认信任任意代理，并会读取 X-Real-IP，这里都关掉）
func ConfigureEngine(r *gin.Engine, trustedProxies []string) error {
	if len(trustedProxies) == 0 {
		trustedProxies = nil
	}
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		return err
	}
	r.RemoteIPHeaders = []string{"X-Forwarded-For"}
	r.Use(Language())
	return nil
}
