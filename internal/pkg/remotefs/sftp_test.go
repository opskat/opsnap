package remotefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/remotefs"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
)

// 起一个真实的 SSH + SFTP 服务，指向临时目录，验证真实链路而不只是纯函数
func startSFTP(t *testing.T, opts sshconntest.SSHOptions) (*ssh.Client, string) {
	t.Helper()
	root := t.TempDir()
	opts.SFTPRoot = root
	srv := sshconntest.StartSSH(t, opts)
	client, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            opts.User,
		Auth:            []ssh.AuthMethod{ssh.Password(opts.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client, root
}

func mkdirs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	}
}

func TestSFTPListRealDirs(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "srv/www/uploads", "srv/www/static", "srv/notes")
	require.NoError(t, os.WriteFile(filepath.Join(root, "srv/www/index.html"), []byte("x"), 0o644))

	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	res, err := remotefs.List(context.Background(), l, "/srv/www")
	require.NoError(t, err)
	require.Len(t, res.List, 2, "只列目录，文件不出现")
	assert.Equal(t, "static", res.List[0].Name)
	assert.Equal(t, "uploads", res.List[1].Name)
	assert.False(t, res.Truncated)
	assert.False(t, res.List[0].Symlink)
}

func TestSFTPListRoot(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "etc", "srv")
	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	res, err := remotefs.List(context.Background(), l, "/")
	require.NoError(t, err)
	assert.Equal(t, "/", res.Path)
	names := make([]string, 0, len(res.List))
	for _, e := range res.List {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "etc")
	assert.Contains(t, names, "srv")
}

func TestSFTPMissingDir(t *testing.T) {
	client, _ := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	_, err = remotefs.List(context.Background(), l, "/nope")
	var re *remotefs.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, remotefs.ReasonNotFound, re.Reason)
}

// 路径是文件而不是目录：必须靠 Stat 判出来，ReadDir 的错误码不可用
func TestSFTPPathIsFile(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "srv"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "srv/a.txt"), []byte("x"), 0o644))

	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	_, err = remotefs.List(context.Background(), l, "/srv/a.txt")
	var re *remotefs.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, remotefs.ReasonNotDir, re.Reason)
}

func TestSFTPPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受权限位限制")
	}
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "secret")
	require.NoError(t, os.Chmod(filepath.Join(root, "secret"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "secret"), 0o755) })

	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	_, err = remotefs.List(context.Background(), l, "/secret")
	var re *remotefs.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, remotefs.ReasonPermission, re.Reason)
}

// 指向目录的符号链接要列出来并标记；备份不跟随，所以必须提示
func TestSFTPSymlinkToDirFlagged(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "data/real")
	require.NoError(t, os.Symlink(filepath.Join(root, "data/real"), filepath.Join(root, "data/link")))

	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	res, err := remotefs.List(context.Background(), l, "/data")
	require.NoError(t, err)
	require.Len(t, res.List, 2)
	assert.Equal(t, "link", res.List[0].Name)
	assert.True(t, res.List[0].Symlink, "指向目录的链接必须标记")
	assert.Equal(t, "real", res.List[1].Name)
	assert.False(t, res.List[1].Symlink)
}

// 断链与指向文件的链接不列
func TestSFTPBrokenSymlinkSkipped(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "data/real")
	require.NoError(t, os.Symlink(filepath.Join(root, "data/nowhere"), filepath.Join(root, "data/broken")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "data/file.txt"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(root, "data/file.txt"), filepath.Join(root, "data/to-file")))

	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	res, err := remotefs.List(context.Background(), l, "/data")
	require.NoError(t, err)
	require.Len(t, res.List, 1)
	assert.Equal(t, "real", res.List[0].Name)
}

func TestSFTPCancelMidList(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "srv/www")
	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = remotefs.List(ctx, l, "/srv/www")
	var re *remotefs.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, remotefs.ReasonTimeout, re.Reason)
}

func TestSFTPStatPath(t *testing.T) {
	client, root := startSFTP(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	mkdirs(t, root, "srv/www")
	require.NoError(t, os.WriteFile(filepath.Join(root, "srv/a.txt"), []byte("x"), 0o644))
	l, closeFn, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFn()

	ctx := context.Background()
	assert.NoError(t, l.Stat(ctx, "/srv/www"))

	err = l.Stat(ctx, "/srv")
	assert.NoError(t, err)

	var nd *remotefs.ErrNotDirectory
	require.ErrorAs(t, l.Stat(ctx, "/srv/a.txt"), &nd)
	assert.ErrorIs(t, l.Stat(ctx, "/nope"), os.ErrNotExist)
}

// 服务器没开 SFTP 子系统时给出可区分的原因
func TestSFTPUnavailable(t *testing.T) {
	srv := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "u", Password: "p"})
	client, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	_, _, err = remotefs.NewSFTP(client)
	require.Error(t, err)
	assert.True(t, errors.Is(err, remotefs.ErrSFTPUnsupported), "没开 sftp 子系统要能区分出来")
}
