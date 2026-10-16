package archtest

import (
	"path/filepath"
	"runtime"
	"testing"
)

const (
	pkgRepository = Module + "/internal/repository"
	pkgService    = Module + "/internal/service"
	pkgController = Module + "/internal/controller"
)

// importBans 是 cago 分层的依赖方向（AGENTS.md「严格遵循 cago 的目录与分层」）
var importBans = []importBanRule{
	{
		dir:       "internal/controller/",
		banned:    []string{pkgRepository, "gorm.io/gorm", "github.com/cago-frame/cago/database/db"},
		skipTests: true,
		message:   "controller 只做校验和转发，不能直接访问仓储或数据库；请经 internal/service 调用（AGENTS.md → 严格遵循 cago 的目录与分层）",
	},
	{
		dir:     "internal/repository/",
		banned:  []string{pkgService, pkgController},
		message: "repository 只做数据访问，不能反向依赖 service/controller（AGENTS.md → 严格遵循 cago 的目录与分层）",
	},
	{
		dir:     "internal/service/",
		banned:  []string{pkgController},
		message: "service 不能依赖 controller（AGENTS.md → 严格遵循 cago 的目录与分层）",
	},
	{
		dir:     "internal/model/",
		banned:  []string{pkgService, pkgController, pkgRepository},
		message: "model/entity 只放数据与简单业务规则，不能依赖 service/controller/repository（AGENTS.md → 严格遵循 cago 的目录与分层）",
	},
}

const (
	msgGoStmt      = "不要用裸 go 语句启动 goroutine；请用 gogo.Go，并且不要把请求的 ctx 传进去（AGENTS.md → 按 cago 的方式处理错误、迁移和并发）"
	msgAutoMigrate = "迁移必须用 gormigrate 编写原生 DDL，不能用 AutoMigrate（AGENTS.md → 按 cago 的方式处理错误、迁移和并发）"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位仓库根目录")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

func scanRepo(t *testing.T) []*parsedFile {
	t.Helper()
	files, err := parseRepo(repoRoot(t))
	if err != nil {
		t.Fatalf("解析仓库失败: %v", err)
	}
	if len(files) < 20 {
		t.Fatalf("只解析到 %d 个 Go 文件，扫描范围不对", len(files))
	}
	return files
}

func TestImportBans(t *testing.T) {
	for _, f := range scanRepo(t) {
		for _, rule := range importBans {
			if !rule.applies(f) {
				continue
			}
			for _, v := range rule.check(f) {
				t.Errorf("%s:%d: %s", v.file, v.line, v.message)
			}
		}
	}
}

func TestNoBareGoStatements(t *testing.T) {
	for _, f := range scanRepo(t) {
		for _, v := range goStmtViolations(f, msgGoStmt) {
			t.Errorf("%s:%d: %s", v.file, v.line, v.message)
		}
	}
}

func TestNoAutoMigrate(t *testing.T) {
	for _, f := range scanRepo(t) {
		for _, v := range callViolations(f, "AutoMigrate", msgAutoMigrate) {
			t.Errorf("%s:%d: %s", v.file, v.line, v.message)
		}
	}
}
