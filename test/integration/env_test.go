//go:build integration

// Package integration 是 opsnap 的集成测试：在 compose 项目 opsnap-it 的内部网络里，
// 通过 opsnap 真实的 HTTP API 驱动真实的 restic、rsync、SSH 代理链和各数据库导出工具。
//
// 只能经 scripts/it.sh（make it）在 opsctl 资产 local-docker 上运行，测试二进制跑在 runner 容器里，
// 不向宿主机发布任何端口。所需的地址和一次性密码都由 compose 经环境变量传入，见 compose.yaml。
package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// env 读取必需的环境变量；缺失时直接让测试失败，避免拿空密码去连真实服务
func env(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("缺少环境变量 %s：集成测试只能经 scripts/it.sh 在 opsnap-it 的 runner 容器里运行", key)
	}
	return v
}

// 测试环境的固定拓扑（服务名即主机名，见 compose.yaml）
const (
	// opsnap 只在 edge 网络上：能到 socks、sftp、postgres，到不了 jump、target 和其他数据库
	hostSOCKS  = "socks"
	hostJump   = "jump"
	hostTarget = "target"
	hostSFTP   = "sftp"

	hostMySQL    = "mysql"
	hostPostgres = "postgres"
	hostRedis    = "redis"
	hostMongo    = "mongo"

	// 恢复导出用的空白实例，只在 verify 网络上，opsnap 看不到
	hostMySQLScratch    = "mysql-scratch"
	hostPostgresScratch = "postgres-scratch"
	hostRedisScratch    = "redis-scratch"
	hostMongoScratch    = "mongo-scratch"

	// runner 与各容器共享的卷
	nasDir          = "/nas"          // opsnap 容器里同样挂在 /nas，本地目录仓库放在这里
	seedFilesDir    = "/seed/files"   // target 容器里只读挂在 targetFilesDir
	targetFilesDir  = "/srv/it-files" // 文件任务要备份的目录（从 target 上看）
	redisScratchDir = "/scratch/redis"

	opsnapUID = 10001 // 镜像里 opsnap 用户的 uid，本地目录仓库要对它可写
)

// waitFor 每秒重试一次 fn，直到成功或超时
func waitFor(t testing.TB, what string, timeout time.Duration, fn func(ctx context.Context) error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		last = fn(ctx)
		cancel()
		if last == nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("等待 %s 超时（%s）：%v", what, timeout, last)
}

// must 把 (值, 错误) 转成「出错即终止测试」：must(f())(t)
func must[T any](v T, err error) func(testing.TB) T {
	return func(t testing.TB) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func check(t testing.TB, err error, format string, args ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s：%v", fmt.Sprintf(format, args...), err)
	}
}
