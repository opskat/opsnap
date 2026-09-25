package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// tempExecCmd 临时目录可执行检查：写入一个临时文件、赋予执行权限、执行、再删除，是本包唯一允许的写操作
const tempExecCmd = `f=$(mktemp) && printf '#!/bin/sh\nexit 0\n' > "$f" && chmod +x "$f" && "$f"; ec=$?; rm -f "$f" 2>/dev/null; exit ${ec:-1}`

// sudoCheckCmd 免密 sudo 检查
const sudoCheckCmd = "sudo -n true"

// serverFileItems 服务器文件的 4 项探测：SSH 可达、CPU 架构、临时目录可执行、免密 sudo
func serverFileItems(ctx context.Context, conn *dsconn.Conn) []Item {
	items := make([]Item, 0, 4)
	items = append(items, decideSSHReachable())
	items = append(items, decideCPUArch(conn.Info.System))

	if stdout, stderr, exitCode, err := sshExec(ctx, conn.SSH, tempExecCmd); err != nil {
		items = append(items, queryErrorItem("server_file.tmp_exec", err))
	} else {
		items = append(items, decideTempDirExec(exitCode == 0, strings.TrimSpace(stderr+stdout)))
	}

	user := ""
	if conn.SSH != nil {
		user = conn.SSH.User()
	}
	if _, stderr, exitCode, err := sshExec(ctx, conn.SSH, sudoCheckCmd); err != nil {
		items = append(items, queryErrorItem("server_file.sudo", err))
	} else {
		items = append(items, decideSudo(exitCode == 0, user, strings.TrimSpace(stderr)))
	}

	return items
}

// sshExec 执行一条命令，返回标准输出、标准错误与退出码。
// 经链路转发的连接不支持读写截止时间（x/crypto/ssh 的限制），ctx 结束时关闭客户端来打断等待。
func sshExec(ctx context.Context, cl *ssh.Client, cmd string) (stdout, stderr string, exitCode int, err error) {
	sess, err := cl.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer func() { _ = sess.Close() }()
	var out, errBuf bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errBuf
	stop := context.AfterFunc(ctx, func() { _ = cl.Close() })
	runErr := sess.Run(cmd)
	if !stop() {
		return "", "", -1, ctx.Err()
	}
	var exit *ssh.ExitError
	switch {
	case errors.As(runErr, &exit):
		return out.String(), errBuf.String(), exit.ExitStatus(), nil
	case runErr != nil:
		return "", "", -1, runErr
	}
	return out.String(), errBuf.String(), 0, nil
}

func decideSSHReachable() Item {
	return Item{
		Key:   "server_file.ssh_reachable",
		Title: itemTitles["server_file.ssh_reachable"],
		Tier:  TierOK,
		Detail: Text{
			ZhCN: "已连接并完成认证",
			En:   "Connected and authenticated.",
		},
	}
}

// decideCPUArch 解析 `uname -sm` 的输出（如 "Linux x86_64"），只认可 amd64 与 arm64；
// 其他架构没有修复方法（无法通过配置改变硬件架构）
func decideCPUArch(system string) Item {
	arch := ""
	if fields := strings.Fields(system); len(fields) > 0 {
		arch = fields[len(fields)-1]
	}
	switch arch {
	case "x86_64", "amd64", "aarch64", "arm64":
		return Item{Key: "server_file.cpu_arch", Title: itemTitles["server_file.cpu_arch"], Tier: TierOK, Detail: Text{
			ZhCN: fmt.Sprintf("%s，属于 amd64 或 arm64", system),
			En:   fmt.Sprintf("%s, which is amd64 or arm64.", system),
		}}
	default:
		return Item{Key: "server_file.cpu_arch", Title: itemTitles["server_file.cpu_arch"], Tier: TierFail, Detail: Text{
			ZhCN: fmt.Sprintf("架构为 %s，不是 amd64 或 arm64，临时执行器不可用", arch),
			En:   fmt.Sprintf("The architecture is %s, neither amd64 nor arm64; the temporary executor is unavailable.", arch),
		}}
	}
}

func decideTempDirExec(ok bool, detail string) Item {
	if ok {
		return Item{Key: "server_file.tmp_exec", Title: itemTitles["server_file.tmp_exec"], Tier: TierOK, Detail: Text{
			ZhCN: "已在临时目录写入并执行了一个测试程序",
			En:   "Wrote and ran a test program in the temp directory.",
		}}
	}
	d := Text{
		ZhCN: "不能在临时目录写入并执行测试程序，临时执行器不可用",
		En:   "Could not write and run a test program in the temp directory; the temporary executor is unavailable.",
	}
	if detail != "" {
		d.ZhCN += "：" + detail
		d.En += ": " + detail
	}
	return Item{Key: "server_file.tmp_exec", Title: itemTitles["server_file.tmp_exec"], Tier: TierFail, Detail: d, Fix: Text{
		ZhCN: "为临时目录去掉 noexec，例如 mount -o remount,exec /tmp",
		En:   "Remove noexec from the temp directory, for example: mount -o remount,exec /tmp",
	}}
}

func decideSudo(ok bool, user, detail string) Item {
	if ok {
		return Item{Key: "server_file.sudo", Title: itemTitles["server_file.sudo"], Tier: TierOK, Detail: Text{
			ZhCN: "sudo -n true 成功",
			En:   "sudo -n true succeeded.",
		}}
	}
	d := Text{
		ZhCN: "sudo -n true 失败，文件系统快照需要 root 权限（直接读取不受影响）",
		En:   "sudo -n true failed; filesystem snapshots need root (direct reads are unaffected).",
	}
	if detail != "" {
		d.ZhCN += "：" + detail
		d.En += ": " + detail
	}
	fix := fmt.Sprintf("在 sudoers 中为该用户添加 NOPASSWD 规则，例如：%s ALL=(ALL) NOPASSWD: ALL", user)
	fixEn := fmt.Sprintf("Add a NOPASSWD rule for this user in sudoers, for example: %s ALL=(ALL) NOPASSWD: ALL", user)
	return Item{Key: "server_file.sudo", Title: itemTitles["server_file.sudo"], Tier: TierWarn, Detail: d, Fix: Text{ZhCN: fix, En: fixEn}}
}
