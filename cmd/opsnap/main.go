package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	_ "github.com/cago-frame/cago/database/db/sqlite"
	"github.com/cago-frame/cago/pkg/component"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/server/mux"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/opskat/opsnap/internal/api"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/pkg/probe"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/oidc_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/repository/system_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
	"github.com/opskat/opsnap/internal/web"
	"github.com/opskat/opsnap/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		os.Exit(runAdmin(os.Args[2:]))
	}

	configFile := flag.String("c", "./configs/config.yaml", "配置文件路径")
	flag.Parse()

	// cago 在组件启动失败时 panic；这里转成一行明确的错误并以非零状态退出，而不是打印调用栈
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("启动失败: %v", r)
		}
	}()

	ctx := context.Background()
	cfg, err := configs.NewConfig("opsnap", configs.WithConfigFile(*configFile))
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	registerRepositories()
	registerHooks()
	// 调试模式下 cago 启用 gin 的访问日志（输出到 gin.DefaultWriter，含查询参数）；
	// 必须在 HTTP 组件创建它之前换成会隐去 OIDC 授权码的输出
	gin.DefaultWriter = middleware.RedactAccessLog(os.Stdout)
	mux.RegisterMiddleware(func(cfg *configs.Config, r *gin.Engine) error {
		var httpCfg struct {
			TrustedProxies []string `yaml:"trustedProxies"`
		}
		if err := cfg.Scan(ctx, "http", &httpCfg); err != nil {
			return err
		}
		if err := middleware.ConfigureEngine(r, httpCfg.TrustedProxies); err != nil {
			return fmt.Errorf("http.trustedProxies 配置错误: %w", err)
		}
		return nil
	})
	mux.RegisterMiddleware(web.Register)

	err = cago.New(ctx, cfg).
		Registry(component.Core()).
		Registry(cago.FuncComponent(ensureDataDir)).
		Registry(component.Database()).
		Registry(cago.FuncComponent(func(ctx context.Context, cfg *configs.Config) error {
			return migrations.RunMigrations(db.Default())
		})).
		Registry(cago.FuncComponent(initSecret)).
		Registry(cago.FuncComponent(func(ctx context.Context, cfg *configs.Config) error {
			// 各存储的 kopia 连接配置与缓存放在 <数据目录>/kopia，删除存储时一并清理
			storage_svc.SetDataDir(dataDir(ctx, cfg))
			return nil
		})).
		Registry(cago.FuncComponent(func(ctx context.Context, cfg *configs.Config) error {
			// 能力探测中主控端工具（mysqldump、pg_dump）在 PATH 之外的备用查找目录，留空则只在 PATH 中查找
			probe.SetToolsDir(toolsDir(ctx, cfg))
			return nil
		})).
		Registry(cago.FuncComponent(printSetupCode)).
		RegistryCancel(mux.HTTP(api.Router)).
		Start()
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// dataDir 数据目录即 SQLite 数据库文件所在目录，master.key 也放在这里
func dataDir(ctx context.Context, cfg *configs.Config) string {
	file, _, _ := strings.Cut(cfg.String(ctx, "db.dsn"), "?")
	return filepath.Dir(file)
}

// ensureDataDir SQLite 不会自动创建数据库文件所在目录
func ensureDataDir(ctx context.Context, cfg *configs.Config) error {
	return os.MkdirAll(dataDir(ctx, cfg), 0o750)
}

// toolsDir 配置项 tools.dir（见 configs/config.example.yaml）：能力探测中主控端工具（mysqldump、
// pg_dump）在 PATH 中找不到时的备用查找目录；未配置时为空
func toolsDir(ctx context.Context, cfg *configs.Config) string {
	return cfg.String(ctx, "tools.dir")
}

// initSecret 加载主密钥；与数据库不匹配时返回错误，阻止服务启动
func initSecret(ctx context.Context, cfg *configs.Config) error {
	res, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{
		DataDir: dataDir(ctx, cfg),
		EnvKey:  os.Getenv(secret.EnvKey),
	})
	if err != nil {
		return err
	}
	if res.Generated {
		logger.Ctx(ctx).Warn("已生成主密钥文件；迁移或恢复 OpsNap 时必须与数据库一起保留，丢失后已保存的凭据无法解密",
			zap.String("path", res.Path))
	}
	return nil
}

// printSetupCode 尚未创建管理员时生成设置码并打印到启动日志，首次设置页需要输入它
func printSetupCode(ctx context.Context, _ *configs.Config) error {
	setupCode, err := auth_svc.Auth().PrepareSetupCode(ctx)
	if err != nil {
		return err
	}
	if setupCode != "" {
		logger.Ctx(ctx).Warn("OpsNap 设置码：" + setupCode + "（尚未创建管理员；在网页的首次设置页输入它。每次重启都会换新）")
	}
	return nil
}

// registerRepositories 注册全部仓库实现；服务与命令行子命令共用
func registerRepositories() {
	system_repo.RegisterSystem(system_repo.NewSystem())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	token_repo.RegisterToken(token_repo.NewToken())
	oidc_repo.RegisterOIDC(oidc_repo.NewOIDC())
	storage_repo.RegisterStorage(storage_repo.NewStorage())
	channel_repo.RegisterChannel(channel_repo.NewChannel())
	datasource_repo.RegisterDataSource(datasource_repo.NewDataSource())
}

// registerHooks 注册模块之间的钩子：通道的引用计数与删除保护计入数据源；通道的主机密钥变化时经过它的数据源同样标记，重新确认后重新测试这些数据源
func registerHooks() {
	datasource_svc.RegisterChannelHooks()
}
