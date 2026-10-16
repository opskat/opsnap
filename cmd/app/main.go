package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata" // 内嵌时区库，镜像里没有系统时区数据也能加载设置中的时区

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/component"
	"github.com/cago-frame/cago/server/mux"

	"github.com/opskat/opsnap/internal/api"
	"github.com/opskat/opsnap/internal/pkg/rsh"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/masterkey_repo"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/job_svc"
	"github.com/opskat/opsnap/internal/service/masterkey_svc"
	"github.com/opskat/opsnap/internal/service/overview_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/run_svc"
	"github.com/opskat/opsnap/internal/service/server_svc"
	"github.com/opskat/opsnap/internal/service/setting_svc"
	"github.com/opskat/opsnap/internal/task/scheduler"
	"github.com/opskat/opsnap/migrations"

	_ "github.com/cago-frame/cago/database/db/sqlite"
)

// defaultDataDir 未在配置里指定 dataDir 时使用的数据目录
const defaultDataDir = "/data"

func main() {
	if code, ok := subcommand(os.Args, os.Getenv, os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	configFile := flag.String("config", "./configs/config.yaml", "配置文件路径")
	flag.Parse()
	if err := run(context.Background(), os.Getenv, *configFile); err != nil {
		log.Printf("opsnap 启动失败: %v", err)
		os.Exit(1)
	}
}

// plannedRuns 把调度器的计划运行接到概览服务（service 层不依赖 internal/task）
func plannedRuns(from, to time.Time) ([]overview_svc.PlannedRun, error) {
	list, err := scheduler.Planned(from, to)
	if err != nil {
		return nil, err
	}
	out := make([]overview_svc.PlannedRun, 0, len(list))
	for _, p := range list {
		out = append(out, overview_svc.PlannedRun{JobID: p.JobID, At: p.At})
	}
	return out, nil
}

// subcommand 处理隐藏子命令。rsh 由运行引擎经 rsync -e 启动，把 rsync 的远端通道接到 opsnap 主进程里
// 已建立的 SSH 连接上（见 internal/pkg/rsh），它不读取主密钥，也不接触任何凭据。
func subcommand(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) (int, bool) {
	if len(args) > 1 && args[1] == rsh.Subcommand {
		return rsh.Main(getenv, args[2:], stdin, stdout, stderr), true
	}
	return 0, false
}

// run 启动服务并阻塞到退出。缺少主密钥时在读取配置、创建数据目录之前直接返回错误。
func run(ctx context.Context, getenv func(string) string, configFile string) error {
	s, err := secret.New(getenv(secret.EnvKey))
	if err != nil {
		return err
	}
	secret.SetDefault(s)

	cfg, err := configs.NewConfig("opsnap", configs.WithConfigFile(configFile))
	if err != nil {
		return fmt.Errorf("读取配置 %s 失败: %w", configFile, err)
	}
	dataDir, err := resolveDataDir(ctx, cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", dataDir, err)
	}

	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	apikey_repo.RegisterAPIKey(apikey_repo.NewAPIKey())
	masterkey_repo.RegisterMasterKey(masterkey_repo.NewMasterKey())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	notify_repo.RegisterNotify(notify_repo.NewNotify())
	server_repo.RegisterServer(server_repo.NewServer())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	job_repo.RegisterJob(job_repo.NewJob())
	run_repo.RegisterRun(run_repo.NewRun())
	alert_repo.RegisterAlert(alert_repo.NewAlert())
	// 任务数用于服务器与存储位置的删除拒绝和列表计数
	server_svc.RegisterJobCounter(job_svc.CountByServer)
	repo_svc.RegisterJobCounter(job_svc.CountByRepo)
	// 运行引擎：文件镜像放在数据目录下；最近一次运行失败的任务标为「需要处理」
	run_svc.RegisterRun(run_svc.NewRun(run_svc.Deps{DataDir: dataDir}))
	setting_svc.SetMirrorDir(filepath.Join(dataDir, "mirror"))
	job_svc.RegisterAttention(run_svc.Attention)
	overview_svc.RegisterOverview(overview_svc.NewOverview(overview_svc.Deps{Planned: plannedRuns}))

	return cago.New(ctx, cfg).
		Registry(component.Core()).
		Registry(component.Database()).
		Registry(cago.FuncComponent(func(ctx context.Context, cfg *configs.Config) error {
			if err := migrations.RunMigrations(db.Default()); err != nil {
				return err
			}
			// 主密钥必须是加密现有凭据的那一把（轮换后要同步修改环境变量），否则拒绝启动
			if err := masterkey_svc.MasterKey().Verify(ctx); err != nil {
				return err
			}
			// 上次进程退出时仍在运行的记录改为失败
			return run_svc.Run().Recover(ctx)
		})).
		// 调度器在迁移之后启动：按计划触发运行、每周维护存储位置、清理过期记录、检查太久没有成功
		Registry(scheduler.Component()).
		RegistryCancel(mux.HTTP(api.Router)).
		Start()
}

// resolveDataDir 读取配置里的数据目录，未配置时用 /data。
// 先用 Has 判断：cago 的文件配置源在读取不存在的键时会回写配置文件。
func resolveDataDir(ctx context.Context, cfg *configs.Config) (string, error) {
	ok, err := cfg.Has(ctx, "dataDir")
	if err != nil {
		return "", err
	}
	if !ok {
		return defaultDataDir, nil
	}
	dir := cfg.String(ctx, "dataDir")
	if dir == "" {
		return "", errors.New("配置项 dataDir 不能为空")
	}
	return dir, nil
}
