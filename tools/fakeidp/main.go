// fakeidp 启动测试用的假 OIDC 提供方（internal/pkg/fakeidp），供冒烟 e2e 使用。
// POST /control/next 可设置下一次授权的表现（见 fakeidp.Behavior）。
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/opskat/opsnap/internal/pkg/fakeidp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18292", "监听地址")
	issuer := flag.String("issuer", "http://127.0.0.1:18292", "对外的 Issuer 地址")
	clientID := flag.String("client-id", "opsnap", "Client ID")
	clientSecret := flag.String("client-secret", "fake-secret", "Client Secret")
	flag.Parse()

	idp, err := fakeidp.New(*issuer, *clientID, *clientSecret)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *addr, Handler: idp.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("fakeidp listening on %s (issuer %s)", *addr, *issuer)
	log.Fatal(srv.ListenAndServe())
}
