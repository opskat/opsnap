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
	"github.com/cago-frame/cago/pkg/utils/httputils"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// ZhCN、En 两种界面语言的 ctx：需要同时保存两种语言的文字（如运行记录）时，分别用它们显示
var (
	ZhCN = i18n.WithLanguage(context.Background(), code.LangZhCN)
	En   = i18n.WithLanguage(context.Background(), code.LangEn)
)

// Pick 按 ctx 的语言在保存的中文与英文之间选择；英文为空（与中文相同，或更早的记录没有英文）时为中文
func Pick(ctx context.Context, zh, en string) string {
	if en != "" && code.Lang(ctx) == code.LangEn {
		return en
	}
	return zh
}

// Localizer 能按 ctx 的界面语言显示的文字
type Localizer interface {
	Localize(ctx context.Context) string
}

// Message 文案编号与参数。参数中的 Localizer（包括 *Error）与 error 按同一语言显示（见 Text），
// 其余（字符串、数字）按文案中的格式原样代入
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
		switch v := a.(type) {
		case Localizer:
			a = v.Localize(ctx)
		case error:
			a = Text(ctx, v)
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

// Text 按 ctx 的语言显示 v：Localizer 用它自己的文案；业务层按错误码给出的接口错误（*httputils.Error）
// 在文案不带参数时按 ctx 的语言重新给出；其余 error（如驱动与库包装过的错误）显示原文，
// 其中包装的本包文字（见 embedded）换成 ctx 的语言；其他值用 fmt 的 %v
func Text(ctx context.Context, v any) string {
	switch e := v.(type) {
	case Localizer:
		return e.Localize(ctx)
	case *httputils.Error:
		if text, ok := codeText(ctx, e); ok {
			return text
		}
		return e.Error()
	case error:
		return embedded(ctx, e, e.Error())
	}
	return fmt.Sprint(v)
}

// embedded 把 text（err 的原文）中出现的、err 包装链上的 Localizer 错误的默认语言文字，换成 ctx 的语言；
// 外层（如驱动）加上的原文不变
func embedded(ctx context.Context, err error, text string) string {
	var inner []error
	switch u := err.(type) { //nolint:errorlint // 逐层展开包装链，只看这一层自己的 Unwrap
	case interface{ Unwrap() error }:
		inner = []error{u.Unwrap()}
	case interface{ Unwrap() []error }:
		inner = u.Unwrap()
	}
	for _, e := range inner {
		if e == nil {
			continue
		}
		if l, ok := e.(Localizer); ok {
			if def := e.Error(); def != "" {
				text = strings.Replace(text, def, l.Localize(ctx), 1)
			}
			continue
		}
		text = embedded(ctx, e, text)
	}
	return text
}

// codeText 接口错误的文案不带参数（原文与某种语言的文案完全相同）时，按 ctx 的语言给出
func codeText(ctx context.Context, e *httputils.Error) (string, bool) {
	for _, lang := range []context.Context{ZhCN, En} {
		if i18n.T(lang, e.Code) == e.Msg {
			return i18n.T(ctx, e.Code), true
		}
	}
	return "", false
}

// Join 按 ctx 的语言用 sep 文案（如顿号与逗号）连接各项
func Join(items []string, sep int) Localizer {
	return Func(func(ctx context.Context) string { return strings.Join(items, i18n.T(ctx, sep)) })
}
