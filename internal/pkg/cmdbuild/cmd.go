// Package cmdbuild 把任务配置翻译成外部命令（restic、rsync、各数据库导出工具）。
//
// 这里只做纯函数式的构建，不执行任何进程。安全约定：
// 密码、密钥、访问令牌等凭据只能出现在 Cmd.Env 或 Cmd.Stdin 里，绝不出现在 Name/Args 中，
// 因此 Cmd.String() 可以直接写进运行日志。
package cmdbuild

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Cmd 描述一次子进程调用
type Cmd struct {
	// Name 程序名（在 PATH 中查找）
	Name string
	// Args 命令行参数，不含凭据
	Args []string
	// Env 额外的环境变量（KEY=VALUE），凭据只能放这里或 Stdin
	Env []string
	// Stdin 写入子进程标准输入的内容；nil 表示不提供标准输入
	Stdin []byte
}

// String 返回可直接写进日志的命令行（参数做 shell 引用；不含 Env 与 Stdin）
func (c Cmd) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, shellQuote(c.Name))
	for _, a := range c.Args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// Secrets 环境变量里凭据的值（按变量名判断：密码、密钥、令牌等），日志和错误信息要遮盖它们；
// 仓库地址、端口、用户名这类普通变量不算，遮盖它们只会把错误信息弄乱
func (c Cmd) Secrets() []string {
	var out []string
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && v != "" && secretEnv(k) {
			out = append(out, v)
		}
	}
	return out
}

// secretEnv 判断环境变量名是否表示凭据
func secretEnv(key string) bool {
	k := strings.ToUpper(key)
	if k == mongoConfigEnv || strings.HasSuffix(k, "_KEY") {
		return true
	}
	for _, s := range []string{"PASS", "PWD", "SECRET", "TOKEN", "AUTH"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// shellQuote 对含有特殊字符的参数加单引号
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !shellSafe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellSafe 判断字符在 shell 中无需引用
func shellSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,+@%", r)
}

// env 拼出一个 KEY=VALUE
func env(key, value string) string {
	return key + "=" + value
}

// checkAbs 校验路径是绝对路径
func checkAbs(what, p string) error {
	if p == "" || !path.IsAbs(p) {
		return fmt.Errorf("%s必须是绝对路径：%q", what, p)
	}
	return nil
}

// checkOperand 校验作为位置参数的值不为空且不会被当成选项
func checkOperand(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s不能为空", what)
	}
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s不能以 - 开头：%q", what, v)
	}
	return nil
}

// checkPort 校验端口范围
func checkPort(port int) error {
	if port <= 0 || port > 65535 {
		return errors.New("端口必须在 1-65535 之间")
	}
	return nil
}
