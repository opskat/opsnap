// Package archtest 把 AGENTS.md / docs/architecture.md 中可判定的分层约束写成 go test 门禁：
// 文档负责描述，这里负责执行。规则表见 importBans，守护测试在文件末尾（违规必须被报告、合规与豁免不被误报）。
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/opskat/opsnap"

type violation struct {
	file    string
	line    int
	message string
}

type parsedFile struct {
	path string // 相对仓库根目录，使用 / 分隔
	fset *token.FileSet
	ast  *ast.File
}

// importBanRule 禁止 dir 前缀下的文件导入 banned 中的包（默认连同子包）
type importBanRule struct {
	dir       string
	banned    []string
	exact     bool
	skipTests bool
	exempt    map[string]bool
	message   string
}

func (r importBanRule) applies(path string) bool {
	if r.dir != "" && !strings.HasPrefix(path, r.dir) {
		return false
	}
	if r.skipTests && strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !r.exempt[path]
}

func bannedImport(path string, banned []string, exact bool) bool {
	for _, b := range banned {
		if path == b || (!exact && strings.HasPrefix(path, b+"/")) {
			return true
		}
	}
	return false
}

func (r importBanRule) check(f *parsedFile) []violation {
	var out []violation
	for _, imp := range f.ast.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if bannedImport(p, r.banned, r.exact) {
			out = append(out, violation{file: f.path, line: f.fset.Position(imp.Pos()).Line, message: r.message})
		}
	}
	return out
}

var importBans = []importBanRule{
	{
		dir:       "internal/controller/",
		banned:    []string{module + "/internal/repository"},
		skipTests: true, // 控制器测试需要注册 mock repository
		message:   "controller 不直接访问 repository，通过对应的 service 调用（docs/architecture.md「分层」）",
	},
	{
		dir:     "internal/service/",
		banned:  []string{module + "/internal/controller"},
		message: "service 不依赖 controller：依赖方向只能是 controller → service → repository（docs/architecture.md「分层」）",
	},
	{
		dir:     "internal/repository/",
		banned:  []string{module + "/internal/controller", module + "/internal/service"},
		message: "repository 不依赖上层：依赖方向只能是 controller → service → repository（docs/architecture.md「分层」）",
	},
	{
		dir: "internal/api/",
		banned: []string{
			module + "/internal/controller", module + "/internal/service", module + "/internal/repository",
		},
		exempt:  map[string]bool{"internal/api/router.go": true}, // 路由注册处需要引用控制器
		message: "internal/api 只放请求与响应定义，不依赖业务层；路由注册集中在 internal/api/router.go（docs/architecture.md）",
	},
	{
		dir:     "internal/",
		banned:  []string{"log"},
		exact:   true, // 只禁标准库 log，不影响 log/slog
		message: "业务代码不用标准库 log，用 cago 的 logger.Ctx(ctx)（docs/develop.md「日志」）",
	},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func parseRepo(t *testing.T) []*parsedFile {
	t.Helper()
	root := repoRoot(t)
	var files []*parsedFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "frontend", "runtime":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, mustParse(t, filepath.ToSlash(rel), p, nil))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func mustParse(t *testing.T, rel, filename string, src any) *parsedFile {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	return &parsedFile{path: rel, fset: fset, ast: f}
}

func TestImportBans(t *testing.T) {
	files := parseRepo(t)
	if len(files) == 0 {
		t.Fatal("没有扫描到任何 Go 文件，检查仓库根目录的定位")
	}
	for _, rule := range importBans {
		for _, f := range files {
			if !rule.applies(f.path) {
				continue
			}
			for _, v := range rule.check(f) {
				t.Errorf("%s:%d: %s", v.file, v.line, v.message)
			}
		}
	}
}

// ---- 守护测试：规则本身必须真的能拦住违规，且不误报 ----

func ruleFor(t *testing.T, dir string) importBanRule {
	t.Helper()
	for _, r := range importBans {
		if r.dir == dir && !r.exact {
			return r
		}
	}
	t.Fatalf("找不到目录 %s 的规则", dir)
	return importBanRule{}
}

func TestGuardCatchesViolation(t *testing.T) {
	cases := []struct{ dir, file, src string }{
		{"internal/controller/", "internal/controller/foo_ctr/foo.go", `package foo_ctr
import "` + module + `/internal/repository/foo_repo"`},
		{"internal/service/", "internal/service/foo_svc/foo.go", `package foo_svc
import "` + module + `/internal/controller/foo_ctr"`},
		{"internal/repository/", "internal/repository/foo_repo/foo.go", `package foo_repo
import "` + module + `/internal/service/foo_svc"`},
		{"internal/api/", "internal/api/foo/foo.go", `package foo
import "` + module + `/internal/service/foo_svc"`},
	}
	for _, c := range cases {
		rule := ruleFor(t, c.dir)
		if !rule.applies(c.file) {
			t.Errorf("规则应作用于 %s", c.file)
		}
		if got := rule.check(mustParse(t, c.file, c.file, c.src)); len(got) == 0 {
			t.Errorf("%s 的违规导入必须被报告", c.file)
		}
	}
	var logRule importBanRule
	for _, r := range importBans {
		if r.exact {
			logRule = r
		}
	}
	f := mustParse(t, "internal/service/foo_svc/foo.go", "foo.go", "package foo_svc\nimport \"log\"")
	if got := logRule.check(f); len(got) == 0 {
		t.Error("标准库 log 必须被报告")
	}
}

func TestGuardAllowsSanctioned(t *testing.T) {
	ctrRule := ruleFor(t, "internal/controller/")
	ok := mustParse(t, "internal/controller/foo_ctr/foo.go", "foo.go", `package foo_ctr
import "`+module+`/internal/service/foo_svc"`)
	if got := ctrRule.check(ok); len(got) != 0 {
		t.Errorf("controller 调用 service 不应被报告: %v", got)
	}
	similar := mustParse(t, "internal/controller/foo_ctr/foo.go", "foo.go", `package foo_ctr
import "`+module+`/internal/repositoryx"`)
	if got := ctrRule.check(similar); len(got) != 0 {
		t.Errorf("前缀相似的无关包不应被报告: %v", got)
	}
	if ctrRule.applies("internal/controller/foo_ctr/foo_test.go") {
		t.Error("控制器测试需要注册 mock repository，不应被检查")
	}
	if ruleFor(t, "internal/api/").applies("internal/api/router.go") {
		t.Error("路由注册文件在豁免名单中，不应被检查")
	}
	for _, r := range importBans {
		if r.exact {
			f := mustParse(t, "internal/service/foo_svc/foo.go", "foo.go", "package foo_svc\nimport \"log/slog\"")
			if got := r.check(f); len(got) != 0 {
				t.Errorf("log/slog 不应被标准库 log 规则误报: %v", got)
			}
		}
	}
}
