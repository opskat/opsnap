// fakepg 供 e2e 使用的假 PostgreSQL（CI 中没有数据库，而新建 PostgreSQL 数据源必须实测连接）：
//
//   - 以 fakepg 运行时是服务端（pgx 的 pgproto3），接受一组明文密码认证，只回答 OpsNap 连接测试、
//     能力探测、库列表与导出前检查用到的查询（见 server.go），列出 app、broken、postgres 三个库；
//   - 以 pg_dump / pg_dumpall 的名字运行时（指向本程序的符号链接，e2e/global-setup.ts 放到 PATH 最前）
//     是假导出工具（见 tool.go）：经 OpsNap 的本机转发端口真的连上服务端，输出满足 internal/pkg/dump
//     完整性检查的内容；导出 broken 库时失败，用于展示失败运行的日志。
package main

import (
	"flag"
	"log"
	"os"
)

func main() {
	if code, ok := runAsTool(os.Args, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	addr := flag.String("addr", "127.0.0.1:0", "监听地址")
	user := flag.String("user", "opsnap", "接受的用户名")
	password := flag.String("password", "", "接受的密码")
	flag.Parse()

	srv, err := Listen(*addr, Config{User: *user, Password: *password})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fakepg listening on %s (PostgreSQL %s)", srv.Addr(), Version)
	select {}
}
