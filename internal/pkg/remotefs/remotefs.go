// Package remotefs 经 SSH 连接读取远端服务器的目录结构。
//
// 只做一件只读的事：列出一层目录里的子目录。不创建、不修改、不删除。
// 所有路径先是字符串，经 Normalize 规范化和校验后才交给 DirLister。
package remotefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxEntries 单层目录返回的条目上限。
//
// 这是返回后的截断，不是读取时的止损：底层 ReadDir 会把整层读进内存才返回，
// 没有分页入口。条目极多的目录层仍可能超时，超时按 ReasonTimeout 报错。
const MaxEntries = 2000

// 浏览失败的原因。前四个是 SFTP 层面的；SSH 链路本身的失败沿用服务器的原因分类。
const (
	ReasonNotFound        = "not_found"        // 目录不存在
	ReasonNotDir          = "not_dir"          // 路径存在但不是目录
	ReasonPermission      = "permission"       // 登录用户没有读权限
	ReasonSFTPUnavailable = "sftp_unavailable" // 服务器没有开启 SFTP 子系统
	ReasonTimeout         = "timeout"          // 服务器响应超时
	ReasonFailed          = "failed"           // 其他失败
)

// Entry 一个目录条目
type Entry struct {
	Name    string
	ModTime time.Time
	IsDir   bool
	// Symlink 指向别处的符号链接。浏览时列出，但备份不会跟随（rsync --archive 不跟随链接）。
	Symlink bool
}

// Item 返回给前端的目录条目
type Item struct {
	Name    string `json:"name"`
	ModTime int64  `json:"mtime"`
	Symlink bool   `json:"symlink"`
	// Selectable 名称含控制字符或不是合法 UTF-8 时为 false：仍然列出（不静默隐藏），
	// 但不能在面板里选，因为写回路径输入框会得到一个无法再读回来的字符串。
	Selectable bool `json:"selectable"`
}

// Result 一次列目录的结果
type Result struct {
	Path      string `json:"path"`
	List      []Item `json:"list"`
	Truncated bool   `json:"truncated"`
}

// DirLister 列出一层目录。真实实现经 SFTP，测试里替换。
type DirLister interface {
	List(ctx context.Context, path string) ([]Entry, error)
}

// Lister 既能列目录，也能确认某条路径存在且是目录。
//
// 两者分开是因为「是不是目录」不能靠 ReadDir 的错误码判定（SFTP 对普通文件
// 返回的是裸 SSH_FX_FAILURE），必须先 Stat。
type Lister interface {
	DirLister
	Stat(ctx context.Context, path string) error
}

// InvalidPathError 路径形状不合法，不进入列举器
type InvalidPathError struct {
	Path   string
	Reason string
}

func (e *InvalidPathError) Error() string {
	return fmt.Sprintf("%q 不是可浏览的路径：%s", e.Path, e.Reason)
}

// ErrNotDirectory 路径存在但不是目录。
//
// 单独成一个类型，因为 SFTP 对普通文件执行 opendir 得到的是裸 SSH_FX_FAILURE，
// 库里的 normaliseError 不会把它映射成 os.ErrNotExist 之外的任何可判定错误，
// 所以「不是目录」只能由列举器先用 Stat 确认后主动返回。
type ErrNotDirectory struct {
	Path string
}

func (e *ErrNotDirectory) Error() string {
	return fmt.Sprintf("%q 不是目录", e.Path)
}

// Error 浏览失败，带可展示的原因
type Error struct {
	Reason  string
	Path    string
	Message string
	// Err 底层错误，供日志与测试判定
	Err error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// Normalize 规范化一层目录路径：合并重复斜杠、去掉结尾斜杠，空串视为根。
//
// 拒绝相对路径、含 `..` 的路径以及含 NUL / 换行的路径——这些会改变要读的位置
// 或让路径无法安全地交给下层，直接归为字段错误，不进 SFTP。
func Normalize(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	switch {
	case !strings.HasPrefix(raw, "/"):
		return "", &InvalidPathError{Path: raw, Reason: "须是以 / 开头的绝对路径"}
	case strings.ContainsAny(raw, "\r\n\x00"):
		return "", &InvalidPathError{Path: raw, Reason: "含非法字符"}
	}
	parts := make([]string, 0, 8)
	for _, seg := range strings.Split(raw, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", &InvalidPathError{Path: raw, Reason: "不能含 .."}
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(parts, "/"), nil
}

// DirsOnly 丢掉非目录条目并按名称排序。
//
// 排序在这做而不是交给服务器：SFTP 的 readdir 顺序由服务器决定，不同机器不一致。
func DirsOnly(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ApplyLimit 截断到 limit 条，并返回是否发生了截断
func ApplyLimit(entries []Entry, limit int) ([]Entry, bool) {
	if len(entries) <= limit {
		return entries, false
	}
	return entries[:limit], true
}

// List 列出 path 这一层的子目录。
//
// 任何错误都整层作废：底层 ReadDir 在传输中途失败时会把已收到的条目连同错误一起返回，
// 把半份列表当完整列表展示会让管理员以为目录里只有这些，从而漏备。
func List(ctx context.Context, l Lister, rawPath string) (*Result, error) {
	path, err := Normalize(rawPath)
	if err != nil {
		return nil, err
	}
	entries, err := l.List(ctx, path)
	if err != nil {
		return nil, classify(path, err)
	}
	dirs := DirsOnly(entries)
	dirs, truncated := ApplyLimit(dirs, MaxEntries)
	items := make([]Item, 0, len(dirs))
	for _, e := range dirs {
		items = append(items, Item{
			Name:       e.Name,
			ModTime:    e.ModTime.Unix(),
			Symlink:    e.Symlink,
			Selectable: selectable(e.Name),
		})
	}
	return &Result{Path: path, List: items, Truncated: truncated}, nil
}

// selectable 名称能不能写回路径输入框并被原样读回
func selectable(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func classify(path string, err error) *Error {
	var notDir *ErrNotDirectory
	var invalid *InvalidPathError
	e := &Error{Path: path, Err: err}
	switch {
	case errors.As(err, &notDir):
		e.Reason = ReasonNotDir
		e.Message = fmt.Sprintf("%s 不是目录，请换一个路径", path)
	case errors.As(err, &invalid):
		e.Reason = ReasonNotFound
		e.Message = invalid.Error()
	case errors.Is(err, fs.ErrNotExist):
		e.Reason = ReasonNotFound
		e.Message = fmt.Sprintf("%s 不存在", path)
	case errors.Is(err, fs.ErrPermission):
		e.Reason = ReasonPermission
		e.Message = fmt.Sprintf("读不了 %s：登录用户没有读权限（Permission denied）", path)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		e.Reason = ReasonTimeout
		e.Message = fmt.Sprintf("读取 %s 超时", path)
	case errors.Is(err, ErrSFTPUnsupported):
		e.Reason = ReasonSFTPUnavailable
		e.Message = "这台服务器没有开启 SFTP，不能浏览目录；可以直接在输入框里填完整路径"
	default:
		e.Reason = ReasonFailed
		e.Message = fmt.Sprintf("读取 %s 失败：%v", path, err)
	}
	return e
}

// ErrSFTPUnsupported 服务器拒绝 sftp 子系统请求
var ErrSFTPUnsupported = errors.New("服务器没有开启 SFTP 子系统")
