package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// runAsTool 以 pg_dump 或 pg_dumpall 的名字（通常是指向 fakepg 的符号链接）运行时充当假导出工具，
// 返回退出码；以其他名字运行时 ok 为 false，由调用方作为服务端启动。
//
// 按 internal/pkg/dump 的调用约定工作：--version 报告与假服务端相同的版本；--dbname 是 libpq 连接串
// （经 hostaddr 连 OpsNap 在本机开的转发端口，密码来自 PGPASSFILE），真的连上服务端并完成认证后才输出：
// pg_dump 输出以 PGDMP 头开始的 custom 格式归档，pg_dumpall --globals-only 以集群导出完成标记结尾。
// 导出 FailingDatabase 时先输出一段归档头，再像服务端读数据块出错那样写错误信息并以 1 退出。
func runAsTool(args []string, stdout, stderr io.Writer) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	name := filepath.Base(args[0])
	if name != "pg_dump" && name != "pg_dumpall" {
		return 0, false
	}
	t := &tool{name: name, stdout: stdout, stderr: stderr}
	return t.run(args[1:]), true
}

type tool struct {
	name           string
	stdout, stderr io.Writer
}

func (t *tool) fail(format string, a ...any) int {
	_, _ = fmt.Fprintf(t.stderr, "%s: error: %s\n", t.name, fmt.Sprintf(format, a...))
	return 1
}

func (t *tool) run(args []string) int {
	opts := map[string]string{}
	flags := map[string]bool{}
	for _, a := range args {
		if a == "--version" {
			_, _ = fmt.Fprintf(t.stdout, "%s (PostgreSQL) %s\n", t.name, Version)
			return 0
		}
		if k, v, found := strings.Cut(a, "="); found {
			opts[k] = v
		} else {
			flags[a] = true
		}
	}
	if t.name == "pg_dump" && opts["--format"] != "custom" {
		return t.fail("fakepg 只支持 --format=custom")
	}
	if t.name == "pg_dumpall" && !flags["--globals-only"] {
		return t.fail("fakepg 只支持 --globals-only")
	}
	connString, found := opts["--dbname"]
	if !found {
		return t.fail("fakepg 需要 --dbname 连接串")
	}
	cfg, err := connConfig(connString, opts["--database"])
	if err != nil {
		return t.fail("invalid connection string: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return t.fail("connection to server failed: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	version := conn.ParameterStatus("server_version")

	if t.name == "pg_dumpall" {
		_, _ = t.stdout.Write(globals(cfg.User, version))
		return 0
	}
	archive := archive(cfg.Database, version)
	if cfg.Database == FailingDatabase {
		// 真实的 pg_dump 在出错前已经写出了归档头
		_, _ = t.stdout.Write(archive[:11])
		_, _ = fmt.Fprintf(t.stderr, "pg_dump: error: Dumping the contents of table \"public.events\" failed: PQgetResult() failed.\n"+
			"pg_dump: detail: Error message from server: ERROR:  could not read block 0 in file \"base/16385/16390\": read only 0 of 8192 bytes\n"+
			"pg_dump: detail: Command was: COPY public.events (id, payload) TO stdout;\n")
		return 1
	}
	_, _ = t.stdout.Write(archive)
	return 0
}

// connConfig 解析 libpq 连接串。pgconn 不认识 hostaddr 与 gssencmode，会把它们当成运行参数发给服务端；
// 这里按 libpq 的语义处理：连接 hostaddr，host 只用于证书校验。连接串没有 dbname 时（pg_dumpall）
// 使用 --database 指定的初始库
func connConfig(connString, initialDB string) (*pgconn.Config, error) {
	cfg, err := pgconn.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	if addr := cfg.RuntimeParams["hostaddr"]; addr != "" {
		for _, fb := range cfg.Fallbacks {
			if fb.Host == cfg.Host {
				fb.Host = addr
			}
		}
		cfg.Host = addr
	}
	delete(cfg.RuntimeParams, "hostaddr")
	delete(cfg.RuntimeParams, "gssencmode")
	if !strings.Contains(connString, "dbname=") && initialDB != "" {
		cfg.Database = initialDB
	}
	return cfg, nil
}

// archive 一份以 custom 格式归档头开始的假归档：PGDMP、归档版本 1.15.0、int 4 字节、offset 8 字节、格式 1（custom）
func archive(db, version string) []byte {
	var b bytes.Buffer
	b.WriteString("PGDMP")
	b.Write([]byte{1, 15, 0, 4, 8, 1})
	_, _ = fmt.Fprintf(&b, "\nfakepg archive of database %q, server PostgreSQL %s\n", db, version)
	for i := range 512 {
		_, _ = fmt.Fprintf(&b, "%s.public.items row %04d\n", db, i)
	}
	return b.Bytes()
}

// globals pg_dumpall --globals-only 的假输出，以完成标记结尾
func globals(user, version string) []byte {
	return []byte(fmt.Sprintf(`--
-- PostgreSQL database cluster dump
--

-- Dumped from database version %[2]s (fakepg)

SET default_transaction_read_only = off;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;

--
-- Roles
--

CREATE ROLE %[1]s;
ALTER ROLE %[1]s WITH SUPERUSER INHERIT CREATEROLE CREATEDB LOGIN REPLICATION BYPASSRLS;

--
-- PostgreSQL database cluster dump complete
--

`, user, version))
}
