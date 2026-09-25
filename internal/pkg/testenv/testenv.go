// Package testenv 供 Go 测试读取 docker.lan 上 opsnap-test 服务的地址与凭据。
// 来源为仓库的 e2e/.env（已被 gitignore），环境变量优先；未配置对应服务时测试跳过并注明原因。
// 变量见 e2e/.env.example，服务定义见 deploy/test/docker-compose.yaml。
package testenv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	// VarHost opsnap-test 所在主机
	VarHost = "TEST_ENV_HOST"
	// VarPassword 所有测试服务共用的密码
	VarPassword = "OPSNAP_TEST_PASSWORD" //nolint:gosec // 这是变量名，不是密码
	// VarMySQLPort MySQL 8.0 对外端口
	VarMySQLPort = "TEST_ENV_MYSQL_PORT"
	// VarPGPort PostgreSQL 16 对外端口
	VarPGPort = "TEST_ENV_PG_PORT"
)

// Service 一个测试服务的地址与凭据
type Service struct {
	Host     string
	Port     int
	User     string
	Password string
}

// Addr host:port
func (s Service) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Env 合并后的配置：环境变量优先，其次为 e2e/.env
type Env struct {
	file   map[string]string
	lookup func(string) (string, bool)
}

// Load 读取仓库根目录（从当前目录向上找 go.mod）下的 e2e/.env；文件不存在时只使用环境变量
func Load() *Env {
	wd, err := os.Getwd()
	if err != nil {
		return newEnv(nil, os.LookupEnv)
	}
	return loadFrom(wd, os.LookupEnv)
}

func loadFrom(dir string, lookup func(string) (string, bool)) *Env {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			f, err := os.Open(filepath.Join(d, "e2e", ".env")) //nolint:gosec // 测试辅助，只读取仓库内的 e2e/.env
			if err != nil {
				return newEnv(nil, lookup)
			}
			defer func() { _ = f.Close() }()
			return newEnv(parse(f), lookup)
		}
		if filepath.Dir(d) == d {
			return newEnv(nil, lookup)
		}
	}
}

func newEnv(file map[string]string, lookup func(string) (string, bool)) *Env {
	return &Env{file: file, lookup: lookup}
}

// parse 解析 KEY=VALUE 行，忽略空行、注释与无法识别的行；值两侧的空白与成对引号会被去掉
func parse(r io.Reader) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !validKey(k) {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		m[k] = v
	}
	return m
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for _, c := range k {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// Get 读取变量，环境变量（即使为空）优先于 e2e/.env
func (e *Env) Get(key string) string {
	if v, ok := e.lookup(key); ok {
		return v
	}
	return e.file[key]
}

// MySQL docker.lan 上的 MySQL 8.0（用户 root）；未配置时错误中列出缺少的变量
func (e *Env) MySQL() (Service, error) { return e.service("MySQL 8.0", VarMySQLPort, "root") }

// Postgres docker.lan 上的 PostgreSQL 16（用户 postgres）；未配置时错误中列出缺少的变量
func (e *Env) Postgres() (Service, error) { return e.service("PostgreSQL 16", VarPGPort, "postgres") }

func (e *Env) service(name, portVar, user string) (Service, error) {
	var missing []string
	for _, k := range []string{VarHost, VarPassword, portVar} {
		if e.Get(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return Service{}, fmt.Errorf("未配置 docker.lan 上的 %s：缺少 %s", name, strings.Join(missing, "、"))
	}
	port, err := strconv.Atoi(e.Get(portVar))
	if err != nil || port < 1 || port > 65535 {
		return Service{}, errors.New(portVar + " 不是有效的端口")
	}
	return Service{Host: e.Get(VarHost), Port: port, User: user, Password: e.Get(VarPassword)}, nil
}

// MySQL 返回 docker.lan 上的 MySQL 8.0；未配置时跳过 t 并注明原因
func MySQL(t testing.TB) Service {
	t.Helper()
	return skipUnless(t, Load().MySQL)
}

// Postgres 返回 docker.lan 上的 PostgreSQL 16；未配置时跳过 t 并注明原因
func Postgres(t testing.TB) Service {
	t.Helper()
	return skipUnless(t, Load().Postgres)
}

func skipUnless(t testing.TB, get func() (Service, error)) Service {
	t.Helper()
	s, err := get()
	if err != nil {
		t.Skipf("%v（在 e2e/.env 或环境变量中配置，见 e2e/.env.example 与 docs/testing.md）", err)
	}
	return s
}
