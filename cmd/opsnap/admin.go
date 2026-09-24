package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/component"
	"golang.org/x/term"

	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/migrations"
)

const adminUsage = `用法：opsnap admin reset-password [-c 配置文件] [--password-stdin]

  reset-password   重置管理员密码，并让所有浏览器登录会话失效（API 令牌不受影响）
                   在交互终端中提示输入新密码（不回显）；非交互环境使用 --password-stdin 从标准输入读取`

// runAdmin 处理 opsnap admin 子命令，返回进程退出码
func runAdmin(args []string) int {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprintln(os.Stderr, adminUsage)
		return 2
	}
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	configFile := fs.String("c", "./configs/config.yaml", "配置文件路径")
	fromStdin := fs.Bool("password-stdin", false, "从标准输入读取新密码")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	ctx := context.Background()
	if err := openDatabase(ctx, *configFile); err != nil {
		fmt.Fprintln(os.Stderr, "打开数据库失败:", err)
		return 1
	}
	fd := int(os.Stdin.Fd())
	rio := resetIO{
		tty:   term.IsTerminal(fd),
		stdin: os.Stdin,
		out:   os.Stdout,
		prompt: func(label string) (string, error) {
			fmt.Fprint(os.Stderr, label)
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(os.Stderr)
			return string(b), err
		},
	}
	if err := resetPassword(ctx, rio, *fromStdin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// openDatabase 只启动日志与数据库组件并执行迁移，不启动 HTTP 服务；服务运行中也可以执行
func openDatabase(ctx context.Context, configFile string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	cfg, err := configs.NewConfig("opsnap", configs.WithConfigFile(configFile))
	if err != nil {
		return err
	}
	registerRepositories()
	cago.New(ctx, cfg).
		Registry(component.Core()).
		Registry(component.Database())
	return migrations.RunMigrations(db.Default())
}

type resetIO struct {
	tty    bool
	stdin  io.Reader
	out    io.Writer
	prompt func(label string) (string, error)
}

func resetPassword(ctx context.Context, rio resetIO, fromStdin bool) error {
	var pw string
	switch {
	case fromStdin:
		line, err := bufio.NewReader(rio.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("读取标准输入: %w", err)
		}
		pw = strings.TrimRight(line, "\r\n")
	case rio.tty:
		first, err := rio.prompt("新密码：")
		if err != nil {
			return err
		}
		second, err := rio.prompt("再次输入新密码：")
		if err != nil {
			return err
		}
		if first != second {
			return errors.New("两次输入的密码不一致，未做任何修改")
		}
		pw = first
	default:
		return errors.New("当前不是交互终端：请使用 --password-stdin 从标准输入提供新密码")
	}

	res, err := auth_svc.Auth().ResetPassword(ctx, pw)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(rio.out, "已重置管理员密码，所有登录会话已失效"); err != nil {
		return err
	}
	if res.PasswordLoginReenabled {
		_, err = fmt.Fprintln(rio.out, "密码登录此前处于关闭状态，已重新开启")
	}
	return err
}
