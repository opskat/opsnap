package sshconntest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/remotefs"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
)

// 一条连接上能同时开多条 SFTP 会话，且会话之间互不影响。
//
// 真实的 jobs/paths 场景就是这样：浏览、保存校验、测试连接可能同时开着会话。
// 这个用例在第一条会话不关的情况下开第二条，两条都要能列目录。
func TestConcurrentSessions(t *testing.T) {
	root := t.TempDir()
	srv := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "u", Password: "p", SFTPRoot: root})

	client, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	// 第一条会话：开起来读一次，故意不关
	first, closeFirst, err := remotefs.NewSFTP(client)
	require.NoError(t, err)
	defer closeFirst()
	_, err = first.List(context.Background(), "/")
	require.NoError(t, err)

	// 第二条会话：必须在超时前能打开并列出目录
	type res struct{ err error }
	done := make(chan res, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		second, closeSecond, err := remotefs.NewSFTP(client)
		if err != nil {
			done <- res{err: err}
			return
		}
		defer closeSecond()
		_, err = second.List(context.Background(), "/")
		done <- res{err: err}
	}()
	select {
	case r := <-done:
		require.NoError(t, r.err, "第二条会话不该被第一条堵住")
	case <-time.After(5 * time.Second):
		t.Fatal("第二条会话被第一条堵住了")
	}
	wg.Wait()
}
