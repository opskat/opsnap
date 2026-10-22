package remotefs

import (
	"context"
	"fmt"
	"os"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/cago-frame/cago/pkg/gogo"
)

// SFTPLister 在既有 SSH 连接上开 SFTP 子系统，列出目录。
//
// 不新建 SSH 连接：浏览复用任务已有的那条链路（含代理链与指纹校验），
// 服务器上也不需要 rsync 或特定 shell。
type SFTPLister struct {
	sc      *sftp.Client
	session interface{ Close() error }
}

// NewSFTP 在 client 上开一个 sftp 子系统会话；调用方必须调用 closeFn。
func NewSFTP(client *ssh.Client) (*SFTPLister, func(), error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("建立 SSH 会话失败：%w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		return nil, nil, fmt.Errorf("建立 SSH 会话失败：%w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		return nil, nil, fmt.Errorf("建立 SSH 会话失败：%w", err)
	}
	// 自己请求子系统，不用 sftp.NewClient：需要区分「服务器没开 SFTP」和别的失败。
	if err := sess.RequestSubsystem("sftp"); err != nil {
		_ = sess.Close()
		return nil, nil, fmt.Errorf("%w（%v）", ErrSFTPUnsupported, err)
	}
	sc, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = sess.Close()
		return nil, nil, fmt.Errorf("启动 SFTP 客户端失败：%w", err)
	}
	l := &SFTPLister{sc: sc, session: sess}
	return l, func() {
		_ = sc.Close()
		_ = sess.Close()
	}, nil
}

// List 先 Stat 确认是目录，再列一层。
//
// 顺序不能颠倒：对普通文件执行 opendir 得到的是裸 SSH_FX_FAILURE，
// pkg/sftp 的 normaliseError 只映射 EOF / NoSuchFile / PermissionDenied，
// 所以「不是目录」必须自己先用 Stat 得出。
func (l *SFTPLister) List(ctx context.Context, path string) ([]Entry, error) {
	if err := l.stat(ctx, path); err != nil {
		return nil, err
	}
	infos, err := l.sc.ReadDirContext(ctx, path)
	if err != nil {
		// 半份列表原样丢弃：ReadDirContext 失败时会把已收到的条目一起返回。
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		link := fi.Mode()&os.ModeSymlink != 0
		if !link && !fi.IsDir() {
			continue
		}
		// 符号链接一律列出来并标记：readdir 的 attrs 是 lstat 语义，
		// 指向目录的链接会显示成「不是目录」，直接丢掉会让管理员以为服务器上没有它。
		// 这里不额外 Stat 去确认目标是不是目录——那是一次往返一个链接，几百个链接就能
		// 把 20 秒预算吃光，整层被判超时。目标类型留到用户点进去时再判定。
		out = append(out, Entry{
			Name:    fi.Name(),
			ModTime: fi.ModTime(),
			IsDir:   true,
			Symlink: link,
		})
	}
	return out, nil
}

// Stat 确认 path 存在且是目录。供保存校验与测试连接使用。
func (l *SFTPLister) Stat(ctx context.Context, path string) error {
	return l.stat(ctx, path)
}

func (l *SFTPLister) stat(ctx context.Context, path string) error {
	// pkg/sftp 的 Stat 不接受 ctx（内部用 context.Background 发包），所以不能在
	// 协程里跑再 select —— 那样超时只是让调用方走开，协程还会挂在一个 TCP RTT 上，
	// 并且拖住 gogo.Wait 的关机路径。
	//
	// 改成：超时到达时主动关掉这次会话，让阻塞中的 Stat 立刻出错返回。
	if err := ctx.Err(); err != nil {
		return err
	}
	type res struct {
		info os.FileInfo
		err  error
	}
	done := make(chan res, 1)
	gogo.Go(func() error {
		info, err := l.sc.Stat(path)
		done <- res{info: info, err: err}
		return nil
	})
	select {
	case <-ctx.Done():
		_ = l.sc.Close()
		return ctx.Err()
	case r := <-done:
		if r.err != nil {
			return r.err
		}
		if !r.info.IsDir() {
			return &ErrNotDirectory{Path: path}
		}
		return nil
	}
}
