// Package archtest 把 AGENTS.md 里可以机器判定的工程原则变成 go test 门禁：
// 文档负责描述，这里的扫描测试负责执行。
// 规则表与全仓扫描在 conventions_test.go；本文件是检查器实现，守卫测试在 checker_test.go。
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Module 是本仓库的 Go 模块路径
const Module = "github.com/opskat/opsnap"

type violation struct {
	file    string // 仓库相对路径（斜杠分隔）
	line    int
	message string
}

type parsedFile struct {
	path string
	fset *token.FileSet
	ast  *ast.File
}

func (f *parsedFile) isTest() bool {
	return strings.HasSuffix(f.path, "_test.go")
}

// isTestSupport 判断是否是只供测试使用的辅助包：按 Go 惯例位于 <父包>/<父包>test 目录，
// 且包名与目录名一致（如 net/http/httptest、sshconn/sshconntest）
func (f *parsedFile) isTestSupport() bool {
	if f.isTest() {
		return true
	}
	dir := path.Dir(f.path)
	base, parent := path.Base(dir), path.Base(path.Dir(dir))
	return f.ast.Name.Name == base && base == parent+"test"
}

// importBanRule 禁止某目录（前缀）下的文件导入指定路径及其子包
type importBanRule struct {
	dir       string          // 仓库相对路径前缀；空表示全仓
	banned    []string        // 导入路径；默认连同子包一起匹配
	skipTests bool            // 是否放过 *_test.go（例如 controller 测试需要注入 mock 仓储）
	exempt    map[string]bool // 仓库相对路径；只减不增
	message   string
}

func (r importBanRule) applies(f *parsedFile) bool {
	if r.dir != "" && !strings.HasPrefix(f.path, r.dir) {
		return false
	}
	if r.skipTests && f.isTest() {
		return false
	}
	return !r.exempt[f.path]
}

// bannedImport 判断导入路径是否命中 banned 本身或其子包；前缀相似的无关包（如 repositoryx）不算命中
func bannedImport(path string, banned []string) bool {
	for _, b := range banned {
		if path == b || strings.HasPrefix(path, b+"/") {
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
		if bannedImport(p, r.banned) {
			out = append(out, violation{file: f.path, line: f.fset.Position(imp.Pos()).Line, message: r.message})
		}
	}
	return out
}

// goStmtViolations 找出业务代码里的裸 go 语句（应改用 gogo.Go）；测试与测试辅助包放过
func goStmtViolations(f *parsedFile, message string) []violation {
	if f.isTestSupport() {
		return nil
	}
	var out []violation
	ast.Inspect(f.ast, func(n ast.Node) bool {
		if g, ok := n.(*ast.GoStmt); ok {
			out = append(out, violation{file: f.path, line: f.fset.Position(g.Pos()).Line, message: message})
		}
		return true
	})
	return out
}

// callViolations 找出非测试代码里对名为 method 的方法/函数的调用（如 AutoMigrate）
func callViolations(f *parsedFile, method, message string) []violation {
	if f.isTest() {
		return nil
	}
	var out []violation
	ast.Inspect(f.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fn.Sel.Name == method {
				out = append(out, violation{file: f.path, line: f.fset.Position(call.Pos()).Line, message: message})
			}
		case *ast.Ident:
			if fn.Name == method {
				out = append(out, violation{file: f.path, line: f.fset.Position(call.Pos()).Line, message: message})
			}
		}
		return true
	})
	return out
}

func parseSource(path, src string) (*parsedFile, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	return &parsedFile{path: path, fset: fset, ast: file}, nil
}

// parseRepo 解析仓库内全部 .go 文件，跳过 vendor、node_modules、隐藏目录、生成的 mock 与 frontend
func parseRepo(root string) ([]*parsedFile, error) {
	var files []*parsedFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "frontend") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(p) // #nosec G304 G122 -- 测试里只读取本仓库源码
		if err != nil {
			return err
		}
		f, err := parseSource(filepath.ToSlash(rel), string(src))
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	return files, err
}
