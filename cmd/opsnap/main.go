package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	_ "github.com/cago-frame/cago/database/db/sqlite"
	"github.com/cago-frame/cago/pkg/component"
	"github.com/cago-frame/cago/server/mux"

	"github.com/opskat/opsnap/internal/api"
	"github.com/opskat/opsnap/internal/repository/system_repo"
	"github.com/opskat/opsnap/internal/web"
	"github.com/opskat/opsnap/migrations"
)

func main() {
	configFile := flag.String("c", "./configs/config.yaml", "配置文件路径")
	flag.Parse()

	ctx := context.Background()
	cfg, err := configs.NewConfig("opsnap", configs.WithConfigFile(*configFile))
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	system_repo.RegisterSystem(system_repo.NewSystem())
	mux.RegisterMiddleware(web.Register)

	err = cago.New(ctx, cfg).
		Registry(component.Core()).
		Registry(cago.FuncComponent(ensureDataDir)).
		Registry(component.Database()).
		Registry(cago.FuncComponent(func(ctx context.Context, cfg *configs.Config) error {
			return migrations.RunMigrations(db.Default())
		})).
		RegistryCancel(mux.HTTP(api.Router)).
		Start()
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// ensureDataDir SQLite 不会自动创建数据库文件所在目录
func ensureDataDir(ctx context.Context, cfg *configs.Config) error {
	file, _, _ := strings.Cut(cfg.String(ctx, "db.dsn"), "?")
	return os.MkdirAll(filepath.Dir(file), 0o750)
}
