//go:build integration

package integration

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const pgDB = "inventory"

func pgConnect(t testing.TB, host, db string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig("")
	check(t, err, "PostgreSQL 连接参数")
	cfg.Host, cfg.Port, cfg.User, cfg.Database = host, 5432, "postgres", db
	cfg.Password = env(t, "IT_PG_PASSWORD")
	cfg.RuntimeParams["TimeZone"] = "UTC"
	cfg.ConnectTimeout = 5 * time.Second
	var conn *pgx.Conn
	waitFor(t, "PostgreSQL "+host, 4*time.Minute, func(ctx context.Context) error {
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return err
		}
		conn = c
		return nil
	})
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func pgExec(t testing.TB, c *pgx.Conn, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		_, err := c.Exec(context.Background(), s)
		check(t, err, "执行 %.80s", s)
	}
}

// seedPostgres 写入虚构数据：两个 schema、序列、数组、jsonb、bytea、numeric、timestamptz、NULL、视图和函数
func seedPostgres(t testing.TB) {
	admin := pgConnect(t, hostPostgres, "postgres")
	pgExec(t, admin, "DROP DATABASE IF EXISTS "+pgDB+" WITH (FORCE)", "CREATE DATABASE "+pgDB)
	c := pgConnect(t, hostPostgres, pgDB)
	pgExec(t, c,
		`CREATE TABLE products (
			id serial PRIMARY KEY,
			sku text UNIQUE NOT NULL,
			name text NOT NULL,
			price numeric(12,2),
			tags text[],
			attrs jsonb,
			image bytea,
			created_at timestamptz NOT NULL
		)`,
		`CREATE TABLE stock_moves (
			id bigserial PRIMARY KEY,
			product_id int NOT NULL REFERENCES products(id),
			qty int NOT NULL,
			moved_at timestamptz NOT NULL,
			note text
		)`,
		`CREATE SCHEMA archive`,
		`CREATE TABLE archive.old_products (sku text PRIMARY KEY, retired_on date NOT NULL, reason text)`,
		`CREATE VIEW low_stock AS
			SELECT p.sku, COALESCE(SUM(m.qty), 0) AS on_hand
			FROM products p LEFT JOIN stock_moves m ON m.product_id = p.id
			GROUP BY p.sku HAVING COALESCE(SUM(m.qty), 0) < 10`,
		`CREATE FUNCTION product_total(pid int) RETURNS bigint LANGUAGE sql STABLE
			AS $$ SELECT COALESCE(SUM(qty), 0) FROM stock_moves WHERE product_id = pid $$`,
	)
	rng := rand.New(rand.NewSource(1015))
	ctx := context.Background()
	tx, err := c.Begin(ctx)
	check(t, err, "开始事务")
	for i := 1; i <= 400; i++ {
		var price, attrs, image, tags any
		if i%6 != 0 {
			price = fmt.Sprintf("%d.%02d", rng.Intn(10000), rng.Intn(100))
		}
		if i%4 != 0 {
			attrs = fmt.Sprintf(`{"color":"色-%d","size":[%d,%d],"fragile":%t,"note":"引号 \" 与反斜杠 \\"}`, i%9, rng.Intn(100), rng.Intn(100), i%2 == 0)
		}
		if i%3 == 0 {
			b := make([]byte, 32+rng.Intn(300))
			rng.Read(b)
			b[0] = 0
			image = b
		}
		if i%5 != 0 {
			tags = []string{"标签", fmt.Sprintf("t%d", i%11), "with,comma", `with "quote"`}
		}
		created := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 53 * time.Minute).Add(time.Duration(i) * time.Microsecond)
		_, err := tx.Exec(ctx, "INSERT INTO products(sku,name,price,tags,attrs,image,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)",
			fmt.Sprintf("SKU-%05d", i), fmt.Sprintf("虚构商品 %d 🙂", i), price, tags, attrs, image, created)
		check(t, err, "写入 products")
	}
	for i := 1; i <= 1500; i++ {
		var note any
		if i%3 != 0 {
			note = fmt.Sprintf("调拨 %d\t制表符\n换行", i)
		}
		_, err := tx.Exec(ctx, "INSERT INTO stock_moves(product_id,qty,moved_at,note) VALUES ($1,$2,$3,$4)",
			1+rng.Intn(400), rng.Intn(50)-20, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i)*time.Hour), note)
		check(t, err, "写入 stock_moves")
	}
	for i := 1; i <= 60; i++ {
		_, err := tx.Exec(ctx, "INSERT INTO archive.old_products VALUES ($1,$2,$3)",
			fmt.Sprintf("OLD-%03d", i), time.Date(2025, 1, i%28+1, 0, 0, 0, 0, time.UTC), fmt.Sprintf("停产原因 %d", i))
		check(t, err, "写入 archive.old_products")
	}
	check(t, tx.Commit(ctx), "提交 PostgreSQL 种子数据")
	// 让序列停在一个非默认的位置，恢复后应一致
	pgExec(t, c, "SELECT setval('products_id_seq', 4242)")
}

// restorePostgres 用 pg_restore 把下载的自定义格式导出恢复到空白实例的同名库
func restorePostgres(t testing.TB, dump []byte) {
	t.Helper()
	admin := pgConnect(t, hostPostgresScratch, "postgres")
	pgExec(t, admin, "DROP DATABASE IF EXISTS "+pgDB+" WITH (FORCE)", "CREATE DATABASE "+pgDB)
	file := filepath.Join(t.TempDir(), pgDB+".dump")
	check(t, os.WriteFile(file, dump, 0o600), "写入导出文件")
	cmd := exec.Command("pg_restore", "--host="+hostPostgresScratch, "--port=5432", "--username=postgres", "--no-password",
		"--exit-on-error", "--dbname="+pgDB, file)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+env(t, "IT_PG_PASSWORD"))
	out, err := cmd.CombinedOutput()
	check(t, err, "pg_restore 恢复下载的导出：%s", out)
}

// pgContent 库的语义内容：每张表按行文本排序的全部行、视图与函数定义、序列位置
func pgContent(t testing.TB, host string) map[string][]string {
	t.Helper()
	c := pgConnect(t, host, pgDB)
	out := map[string][]string{}
	tables := pgStrings(t, c, `SELECT table_schema || '.' || table_name || ':' || table_type FROM information_schema.tables
		WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY 1`)
	out["tables"] = tables
	for _, tt := range tables {
		var name, typ string
		for i := len(tt) - 1; i >= 0; i-- {
			if tt[i] == ':' {
				name, typ = tt[:i], tt[i+1:]
				break
			}
		}
		if typ != "BASE TABLE" {
			continue
		}
		out["rows:"+name] = pgStrings(t, c, "SELECT x::text FROM "+name+" x ORDER BY 1")
	}
	out["views"] = pgStrings(t, c, `SELECT schemaname || '.' || viewname || ' ' || definition FROM pg_views
		WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY 1`)
	out["functions"] = pgStrings(t, c, `SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname NOT IN ('pg_catalog','information_schema') ORDER BY p.proname`)
	out["sequences"] = pgStrings(t, c, `SELECT schemaname || '.' || sequencename || '=' || COALESCE(last_value::text, 'null') FROM pg_sequences ORDER BY 1`)
	out["constraints"] = pgStrings(t, c, `SELECT conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint
		WHERE connamespace::regnamespace::text NOT IN ('pg_catalog','information_schema') ORDER BY 1`)
	return out
}

func pgStrings(t testing.TB, c *pgx.Conn, q string) []string {
	t.Helper()
	rows, err := c.Query(context.Background(), q)
	check(t, err, "查询 %.80s", q)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		check(t, rows.Scan(&s), "读取 %.80s", q)
		out = append(out, s)
	}
	check(t, rows.Err(), "遍历 %.80s", q)
	return out
}
