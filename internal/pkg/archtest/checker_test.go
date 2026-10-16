package archtest

import (
	"testing"
)

// 守卫测试：经由真实规则表断言两个方向——违规必须报出，合规与豁免不能报出

func mustParse(t *testing.T, path, src string) *parsedFile {
	t.Helper()
	f, err := parseSource(path, src)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func findRule(t *testing.T, dir string) importBanRule {
	t.Helper()
	for _, r := range importBans {
		if r.dir == dir {
			return r
		}
	}
	t.Fatalf("规则表里没有 %s 的规则", dir)
	return importBanRule{}
}

func TestCheckerCatchesLayerViolation(t *testing.T) {
	rule := findRule(t, "internal/controller/")
	f := mustParse(t, "internal/controller/foo_ctr/foo.go", `
package foo_ctr
import "`+pkgRepository+`/server_repo"
`)
	if !rule.applies(f) {
		t.Fatal("规则应作用于 internal/controller 下的文件")
	}
	if got := rule.check(f); len(got) == 0 {
		t.Error("controller 直接导入仓储必须报出")
	}
	g := mustParse(t, "internal/controller/foo_ctr/foo.go", `
package foo_ctr
import "gorm.io/gorm"
`)
	if got := rule.check(g); len(got) == 0 {
		t.Error("controller 直接导入 gorm 必须报出")
	}
	m := mustParse(t, "internal/model/entity/foo_entity/foo.go", `
package foo_entity
import "`+pkgService+`/foo_svc"
`)
	if got := findRule(t, "internal/model/").check(m); len(got) == 0 {
		t.Error("entity 依赖 service 必须报出")
	}
}

func TestCheckerAllowsSanctioned(t *testing.T) {
	rule := findRule(t, "internal/controller/")
	ok := mustParse(t, "internal/controller/foo_ctr/foo.go", `
package foo_ctr
import "`+pkgService+`/foo_svc"
`)
	if got := rule.check(ok); len(got) != 0 {
		t.Errorf("合规导入不应报出: %v", got)
	}
	similar := mustParse(t, "internal/controller/foo_ctr/bar.go", `
package foo_ctr
import "`+pkgRepository+`x/foo"
`)
	if got := rule.check(similar); len(got) != 0 {
		t.Errorf("前缀相似的无关包不应报出: %v", got)
	}
	test := mustParse(t, "internal/controller/foo_ctr/foo_test.go", `
package foo_ctr
import "`+pkgRepository+`/foo_repo/mock_foo_repo"
`)
	if rule.applies(test) {
		t.Error("controller 测试需要注入 mock 仓储，不应被检查")
	}
}

func TestGoStmtChecker(t *testing.T) {
	bad := mustParse(t, "internal/service/foo_svc/foo.go", `
package foo_svc
func f() { go func() {}() }
`)
	if got := goStmtViolations(bad, msgGoStmt); len(got) != 1 {
		t.Errorf("裸 go 语句必须报出 1 处，实际 %d", len(got))
	}
	good := mustParse(t, "internal/service/foo_svc/foo.go", `
package foo_svc
import "github.com/cago-frame/cago/pkg/gogo"
func f() { gogo.Go(func() error { return nil }) }
`)
	if got := goStmtViolations(good, msgGoStmt); len(got) != 0 {
		t.Errorf("gogo.Go 不应报出: %v", got)
	}
	test := mustParse(t, "internal/service/foo_svc/foo_test.go", `
package foo_svc
func f() { go func() {}() }
`)
	if got := goStmtViolations(test, msgGoStmt); len(got) != 0 {
		t.Errorf("测试文件不应报出: %v", got)
	}
	support := mustParse(t, "internal/pkg/sshconn/sshconntest/server.go", `
package sshconntest
func f() { go func() {}() }
`)
	if got := goStmtViolations(support, msgGoStmt); len(got) != 0 {
		t.Errorf("测试辅助包不应报出: %v", got)
	}
	notSupport := mustParse(t, "internal/pkg/latest/x.go", `
package latest
func f() { go func() {}() }
`)
	if got := goStmtViolations(notSupport, msgGoStmt); len(got) != 1 {
		t.Errorf("包名只是含 test 的业务包仍须报出 1 处，实际 %d", len(got))
	}
}

func TestAutoMigrateChecker(t *testing.T) {
	bad := mustParse(t, "migrations/202601010001_x.go", `
package migrations
func m(tx interface{ AutoMigrate(...any) error }) error { return tx.AutoMigrate() }
`)
	if got := callViolations(bad, "AutoMigrate", msgAutoMigrate); len(got) != 1 {
		t.Errorf("AutoMigrate 调用必须报出 1 处，实际 %d", len(got))
	}
	good := mustParse(t, "migrations/202601010001_x.go", `
package migrations
// 不要在迁移里使用 AutoMigrate
func m(tx interface{ Exec(string) error }) error { return tx.Exec("CREATE TABLE t (id INTEGER)") }
`)
	if got := callViolations(good, "AutoMigrate", msgAutoMigrate); len(got) != 0 {
		t.Errorf("原生 DDL 与注释不应报出: %v", got)
	}
}
