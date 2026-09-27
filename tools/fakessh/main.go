// fakessh 启动测试用的假 SSH 服务端（internal/pkg/fakessh），供 e2e 使用：
// 作为网络通道的 SSH 跳板，或服务器文件数据源的目标主机。
// 对 `uname -sm` 之外，还对能力探测用到的临时目录可执行检查与 `sudo -n true` 给出确定的输出，
// 使探测产生看得见的结果（3 项通过、免密 sudo 一项提醒）。
// 可选的 -control-addr 启动一个控制 HTTP 端口：POST /rotate-host-key 换一把新的主机密钥，
// 用于在 e2e 中模拟“主机密钥已变化”。
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/opskat/opsnap/internal/pkg/fakessh"
)

// tempExecMarker 是探测器临时目录可执行检查命令的特征片段
// （见 internal/pkg/probe/serverfile.go 未导出的 tempExecCmd：mktemp && ... && rm -f），
// 这里不依赖该未导出常量，只按子串识别，给出确定的成功结果
const tempExecMarker = "mktemp"

// sudoCheckCmd 免密 sudo 检查命令（同上，probe/serverfile.go 的 sudoCheckCmd）
const sudoCheckCmd = "sudo -n true"

// fakeExec 让探测的 4 项服务器文件检查都有结果：SSH 可达（由已完成认证本身体现）、
// CPU 架构（uname -sm 走默认实现）、临时目录可执行（总是成功）、免密 sudo（总是失败，得到一条提醒）
func fakeExec(cmd string) (stdout, stderr string, exitCode int) {
	switch {
	case strings.Contains(cmd, tempExecMarker):
		return "", "", 0
	case strings.TrimSpace(cmd) == sudoCheckCmd:
		return "", "sudo: a password is required\n", 1
	default:
		return fakessh.DefaultExec(cmd)
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "监听地址")
	user := flag.String("user", "opsnap", "接受的用户名")
	password := flag.String("password", "", "接受的密码，为空则不接受密码认证")
	controlAddr := flag.String("control-addr", "", "可选的控制 HTTP 地址；POST /rotate-host-key 换一把新的主机密钥")
	flag.Parse()

	srv, err := fakessh.Listen(*addr, fakessh.Config{User: *user, Password: *password, Exec: fakeExec})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fakessh listening on %s (fingerprint %s)", srv.Addr(), srv.Fingerprint())

	if *controlAddr == "" {
		select {}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rotate-host-key", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
			return
		}
		if err := srv.RotateHostKey(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("fakessh host key rotated, new fingerprint %s", srv.Fingerprint())
		w.WriteHeader(http.StatusNoContent)
	})
	ctrl := &http.Server{Addr: *controlAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("fakessh control listening on %s", *controlAddr)
	log.Fatal(ctrl.ListenAndServe())
}
