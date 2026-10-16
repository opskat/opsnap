package snapshot_ctr

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 客户端停止读取（下载被暂停）时，写入要在空闲时限后失败，下载随之结束、释放存储位置的共享锁
func TestDownloadWriterFailsWhenClientStopsReading(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writeErr := make(chan error, 1)
	r := gin.New()
	r.GET("/dl", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		w := idleWriter(c.Writer, 200*time.Millisecond)
		chunk := make([]byte, 1<<20)
		for {
			if _, err := w.Write(chunk); err != nil {
				writeErr <- err
				return
			}
		}
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.Write([]byte("GET /dl HTTP/1.1\r\nHost: x\r\n\r\n"))
	require.NoError(t, err)
	// 只读响应头，之后不再读取
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	select {
	case err := <-writeErr:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("客户端不读取时写入一直阻塞，下载不会结束")
	}
}
