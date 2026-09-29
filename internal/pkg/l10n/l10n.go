// Package l10n 表示可以按界面语言显示的文字与错误：保存文案编号（internal/pkg/code）与参数，
// 在显示时才按 ctx 的语言拼出文字。参数中的原文（导出工具的错误输出、驱动错误等）原样代入，不翻译。
//
// 用在需要先产生、之后再按查看者语言显示的文字上，例如运行记录的失败原因与执行日志；
// Error() 与 String() 为默认语言（中文），与改用本包之前的文字一致。
package l10n

import (
	"context"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
)

// Localizer 能按 ctx 的界面语言显示的文字
type Localizer interface {
	Localize(ctx context.Context) string
}

// Message 文案编号与参数。参数中的 Localizer（包括 *Error）按同一语言显示，其余（字符串、数字、
// 其他 error）按文案中的格式原样代入
type Message struct {
	Code int
	Args []any
}

// New 一条文字
func New(code int, args ...any) Message { return Message{Code: code, Args: args} }

// Localize 按 ctx 的语言显示
func (m Message) Localize(ctx context.Context) string {
	return i18n.T(ctx, m.Code, localizeArgs(ctx, m.Args)...)
}

// String 默认语言（中文）的文字
func (m Message) String() string { return m.Localize(context.Background()) }

func localizeArgs(ctx context.Context, args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		if l, ok := a.(Localizer); ok {
			a = l.Localize(ctx)
		}
		out[i] = a
	}
	return out
}

// Plain 不需要翻译的文字（如导出工具的一行错误输出）
type Plain string

func (p Plain) Localize(context.Context) string { return string(p) }

// Func 显示时才计算的文字
type Func func(ctx context.Context) string

func (f Func) Localize(ctx context.Context) string { return f(ctx) }

// Error 以文案表示的错误。Error() 为默认语言（中文）；参数中的 error 都被包装（与 fmt.Errorf 的 %w 相同），
// errors.Is / errors.As 能找到它们
type Error struct {
	Message
}

// Errorf 一个以文案表示的错误；args 中的 error 被包装
func Errorf(code int, args ...any) *Error { return &Error{Message: New(code, args...)} }

func (e *Error) Error() string { return e.String() }

func (e *Error) Unwrap() []error {
	var errs []error
	for _, a := range e.Args {
		if err, ok := a.(error); ok && err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Text 按 ctx 的语言显示 v：Localizer 用它自己的文案，其余（如不是本包的 error）用 fmt 的 %v
func Text(ctx context.Context, v any) string {
	if l, ok := v.(Localizer); ok {
		return l.Localize(ctx)
	}
	return fmt.Sprint(v)
}

// Join 按 ctx 的语言用 sep 文案（如顿号与逗号）连接各项
func Join(items []string, sep int) Localizer {
	return Func(func(ctx context.Context) string { return strings.Join(items, i18n.T(ctx, sep)) })
}
