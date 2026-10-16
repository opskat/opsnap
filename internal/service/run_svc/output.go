package run_svc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

// resticResult 一次 restic backup 的结果，从 --json 输出的 summary 中读取
type resticResult struct {
	snapshotID string
	processed  int64
	added      int64
}

// resticMessage restic --json 输出的一行
type resticMessage struct {
	MessageType         string          `json:"message_type"`
	SnapshotID          string          `json:"snapshot_id"`
	DataAdded           int64           `json:"data_added"`
	TotalBytesProcessed int64           `json:"total_bytes_processed"`
	PercentDone         float64         `json:"percent_done"`
	BytesDone           int64           `json:"bytes_done"`
	Error               json.RawMessage `json:"error"`
	During              string          `json:"during"`
	Item                string          `json:"item"`
	Message             string          `json:"message"`
	Code                int             `json:"code"`
}

// errorText restic 的 error 字段在不同版本里是对象 {"message":…} 或字符串
func (m resticMessage) errorText() string {
	if len(m.Error) == 0 {
		return ""
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(m.Error, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	var s string
	if json.Unmarshal(m.Error, &s) == nil {
		return s
	}
	return string(m.Error)
}

// resticWriter 解析 restic backup --json 的输出：记下快照 ID 与数据量，把 status 里的已处理字节数交给 onProgress，把进度、错误写成可读的日志
func (l *liveLog) resticWriter(res *resticResult, onProgress func(done int64)) *lineWriter {
	return newLineWriter(func(line string) {
		var m resticMessage
		if !strings.HasPrefix(strings.TrimSpace(line), "{") || json.Unmarshal([]byte(line), &m) != nil || m.MessageType == "" {
			l.add(textLevel(line), line)
			return
		}
		switch m.MessageType {
		case "summary":
			res.snapshotID = m.SnapshotID
			res.processed = m.TotalBytesProcessed
			res.added = m.DataAdded
			l.infof("restic 完成：处理 %d 字节，新增 %d 字节，快照 %s", m.TotalBytesProcessed, m.DataAdded, m.SnapshotID)
		case "status":
			if onProgress != nil {
				onProgress(m.BytesDone)
			}
			l.infof("进度 %.1f%%", m.PercentDone*100)
		case "error":
			msg := m.errorText()
			if m.Item != "" {
				msg = m.Item + "：" + msg
			}
			l.warnf("restic 报错（%s）：%s", m.During, msg)
		case "exit_error":
			l.errorf("restic 退出（%d）：%s", m.Code, m.Message)
		case "verbose_status":
		default:
			l.infof("%s", line)
		}
	})
}

// capBuffer 收集命令输出供解析，超过上限后丢弃，避免占满内存
type capBuffer struct {
	buf bytes.Buffer
	max int
}

func newCapBuffer() *capBuffer { return &capBuffer{max: 8 << 20} }

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// tail 最后几行，用作失败原因的补充
func (c *capBuffer) tail() string {
	lines := strings.Split(strings.TrimSpace(c.buf.String()), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	s := strings.Join(lines, " / ")
	if len(s) > 500 {
		s = s[len(s)-500:]
	}
	return s
}

// parseSnapshotIDs 解析 restic snapshots --json 的输出
func parseSnapshotIDs(out []byte) []string {
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &list); err != nil {
		return nil
	}
	ids := make([]string, 0, len(list))
	for _, s := range list {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// countRemoved 解析 restic forget --json 的输出，返回删除的快照数
func countRemoved(out []byte) int {
	var groups []struct {
		Remove []json.RawMessage `json:"remove"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &groups); err != nil {
		return -1
	}
	n := 0
	for _, g := range groups {
		n += len(g.Remove)
	}
	return n
}

var snapshotIDPattern = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// forgetIDs 生成 `restic forget -- <快照 ID>…`，删除指定的快照（失败运行的回滚）。
// cmdbuild 只提供按保留规则的 forget，这里沿用它为同一仓库生成的地址、环境变量与全局参数，只换子命令和操作数。
func forgetIDs(r cmdbuild.Repo, ids []string) (cmdbuild.Cmd, error) {
	if len(ids) == 0 {
		return cmdbuild.Cmd{}, errors.New("没有要删除的快照")
	}
	for _, id := range ids {
		if !snapshotIDPattern.MatchString(id) {
			return cmdbuild.Cmd{}, fmt.Errorf("快照 ID 不合法：%q", id)
		}
	}
	c, err := cmdbuild.ResticSnapshots(r, nil)
	if err != nil {
		return cmdbuild.Cmd{}, err
	}
	if len(c.Args) == 0 || c.Args[0] != "snapshots" {
		return cmdbuild.Cmd{}, errors.New("restic 命令格式不符合预期")
	}
	c.Args[0] = "forget"
	c.Args = append(append(c.Args, "--"), ids...)
	return c, nil
}

// uniqueIDs 去重并排序
func uniqueIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// rsyncProgress rsync --info=progress2 的进度行：已传输字节、百分比、速度、剩余时间或用时（算不出时为 ??:??:??），
// 可能带「(xfr#3, to-chk=10/50)」。字节数默认带千位分隔符（逗号，或本地化下的句点），附加参数带 -h 时是 1.23M 这样带单位的近似值
var rsyncProgress = regexp.MustCompile(`^\s*([\d.,]+)([KMGTPE]?)\s+\d+%\s+\S+/s\s+(?:\d+:\d{2}:\d{2}|\?\?:\?\?:\?\?)(?:\s+\(xfr#\d+, [a-z]+-chk=\d+/\d+\))?`)

// rsyncWriter rsync 的输出：进度行（以 \r 分隔）只交给 onProgress，不写日志，其余照常写日志
func (l *liveLog) rsyncWriter(onProgress func(bytes int64)) *lineWriter {
	w := newLineWriter(func(line string) {
		if m := rsyncProgress.FindStringSubmatch(line); m != nil {
			// 带单位的是近似值，不报告；分隔符去掉后就是精确的字节数
			if m[2] == "" {
				if n, err := strconv.ParseInt(strings.NewReplacer(",", "", ".", "").Replace(m[1]), 10, 64); err == nil {
					onProgress(n)
				}
			}
			// 紧跟在进度行后面、没有换行隔开的输出照常写日志
			line = line[len(m[0]):]
		}
		if strings.TrimSpace(line) == "" {
			return
		}
		l.add(textLevel(line), line)
	})
	w.crIsNewline = true
	return w
}
