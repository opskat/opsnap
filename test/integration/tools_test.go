//go:build integration

package integration

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestImageTools 镜像里的外部工具齐全且版本满足 spec 的要求。
// runner 镜像以 opsnap 镜像为基础，二进制完全相同；scripts/it-remote.sh 另在 opsnap 容器里打印同一组版本。
func TestImageTools(t *testing.T) {
	// pg_dump 的大版本不能低于要备份的服务器
	var serverMajor string
	c := pgConnect(t, hostPostgres, "postgres")
	check(t, c.QueryRow(context.Background(), "SHOW server_version").Scan(&serverMajor), "读取 PostgreSQL 版本")
	serverMajor, _, _ = strings.Cut(serverMajor, ".")

	cases := []struct {
		cmd     []string
		pattern string // 第一个分组是版本号
		min     string
	}{
		{[]string{"restic", "version"}, `restic (\d+\.\d+\.\d+)`, "0.17.0"},
		{[]string{"rclone", "version"}, `rclone v(\d+\.\d+\.\d+)`, "1.60.0"},
		{[]string{"rsync", "--version"}, `rsync\s+version (\d+\.\d+\.\d+)`, "3.1.0"},
		{[]string{"ssh", "-V"}, `OpenSSH_(\d+\.\d+)`, "8.0"},
		{[]string{"mysqldump", "--version"}, `Ver (\d+\.\d+\.\d+)`, "8.0.0"},
		{[]string{"mariadb-dump", "--version"}, `Distrib (\d+\.\d+\.\d+)-MariaDB`, "10.5.0"},
		{[]string{"pg_dump", "--version"}, `\(PostgreSQL\) (\d+)`, serverMajor},
		{[]string{"mongodump", "--version"}, `mongodump version: (\d+\.\d+\.\d+)`, "100.3.0"},
		{[]string{"redis-cli", "--version"}, `redis-cli (\d+\.\d+\.\d+)`, "7.0.0"},
		{[]string{"sh", "-c", "echo sh-ok"}, `(sh-ok)`, ""},
	}
	for _, tc := range cases {
		out, err := exec.Command(tc.cmd[0], tc.cmd[1:]...).CombinedOutput()
		check(t, err, "运行 %v：%s", tc.cmd, out)
		m := regexp.MustCompile(tc.pattern).FindStringSubmatch(string(out))
		if m == nil {
			t.Errorf("%v 的输出里找不到版本：%s", tc.cmd, out)
			continue
		}
		t.Logf("%s %s", tc.cmd[0], m[1])
		if tc.min != "" && compareVersion(m[1], tc.min) < 0 {
			t.Errorf("%s 版本 %s 低于要求的 %s", tc.cmd[0], m[1], tc.min)
		}
	}
}

// compareVersion 按点分数字比较版本号
func compareVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
