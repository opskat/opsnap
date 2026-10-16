//go:build integration

package integration

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	mysqlDB       = "shop"        // 完整导出、恢复后比对
	mysqlBrokenDB = "shop_broken" // 导出到一半失败：先导出表数据，再遇到失效的视图报错
)

func mysqlOpen(t testing.TB, host, db string) *sql.DB {
	t.Helper()
	cfg := mysql.NewConfig()
	cfg.User = "root"
	cfg.Passwd = env(t, "IT_MYSQL_PASSWORD")
	cfg.Net = "tcp"
	cfg.Addr = host + ":3306"
	cfg.DBName = db
	cfg.ParseTime = false
	cfg.Collation = "utf8mb4_0900_ai_ci"
	conn, err := mysql.NewConnector(cfg)
	check(t, err, "MySQL 连接参数")
	d := sql.OpenDB(conn)
	// 只用一条连接：种子脚本里的 USE 只对当前连接生效
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = d.Close() })
	waitFor(t, "MySQL "+host, 4*time.Minute, func(ctx context.Context) error { return d.PingContext(ctx) })
	return d
}

func mysqlExec(t testing.TB, d *sql.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		_, err := d.Exec(s)
		check(t, err, "执行 %.80s", s)
	}
}

// seedMySQL 写入虚构数据：中文与 emoji、NULL、定点数、二进制（含 0x00、引号、反斜杠）、JSON、外键、
// 触发器、视图、存储过程；另建 shop_broken，其中的视图引用已删除的表，mysqldump 导出到它时会中途失败
func seedMySQL(t testing.TB) {
	d := mysqlOpen(t, hostMySQL, "")
	mysqlExec(t, d,
		"DROP DATABASE IF EXISTS "+mysqlDB,
		"DROP DATABASE IF EXISTS "+mysqlBrokenDB,
		"CREATE DATABASE "+mysqlDB+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci",
		"USE "+mysqlDB,
		`CREATE TABLE customers (
			id INT PRIMARY KEY AUTO_INCREMENT,
			name VARCHAR(100) NOT NULL,
			email VARCHAR(200) NULL,
			balance DECIMAL(12,2) NOT NULL,
			note TEXT NULL,
			avatar BLOB NULL,
			created_at DATETIME(3) NOT NULL,
			UNIQUE KEY uk_email (email)
		) ENGINE=InnoDB`,
		`CREATE TABLE orders (
			id INT PRIMARY KEY AUTO_INCREMENT,
			customer_id INT NOT NULL,
			amount DECIMAL(10,2) NOT NULL,
			status ENUM('new','paid','shipped') NOT NULL,
			payload JSON NULL,
			CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers(id)
		) ENGINE=InnoDB`,
		`CREATE TABLE order_audit (
			id INT PRIMARY KEY AUTO_INCREMENT,
			order_id INT NOT NULL,
			action VARCHAR(20) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TRIGGER trg_orders_audit AFTER INSERT ON orders FOR EACH ROW
			INSERT INTO order_audit(order_id, action) VALUES (NEW.id, 'insert')`,
		`CREATE VIEW paid_orders AS SELECT o.id, c.name, o.amount FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.status = 'paid'`,
		`CREATE PROCEDURE customer_count(OUT n INT) BEGIN SELECT COUNT(*) INTO n FROM customers; END`,
	)
	rng := rand.New(rand.NewSource(20261015))
	tx, err := d.Begin()
	check(t, err, "开始事务")
	for i := 1; i <= 300; i++ {
		var email, note any
		if i%7 != 0 {
			email = fmt.Sprintf("user%03d@example.invalid", i)
		}
		if i%5 != 0 {
			note = fmt.Sprintf("备注 #%d：O'Brien \"quoted\" back\\slash 🙂 Ünïcødé", i)
		}
		var avatar any
		if i%3 == 0 {
			b := make([]byte, 64+rng.Intn(512))
			rng.Read(b)
			b[0], b[1], b[2] = 0x00, '\'', '\\'
			avatar = b
		}
		created := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC).Add(time.Duration(i) * 97 * time.Minute).Add(time.Duration(i) * time.Millisecond)
		_, err := tx.Exec("INSERT INTO "+mysqlDB+".customers(name,email,balance,note,avatar,created_at) VALUES (?,?,?,?,?,?)",
			fmt.Sprintf("测试客户-%03d", i), email, fmt.Sprintf("%d.%02d", rng.Intn(100000), rng.Intn(100)), note, avatar,
			created.Format("2006-01-02 15:04:05.000"))
		check(t, err, "写入 customers")
	}
	statuses := []string{"new", "paid", "shipped"}
	for i := 1; i <= 800; i++ {
		var payload any
		if i%4 != 0 {
			payload = fmt.Sprintf(`{"sku":"SKU-%04d","qty":%d,"gift":%t,"memo":"第 %d 单"}`, rng.Intn(9999), 1+rng.Intn(9), i%2 == 0, i)
		}
		_, err := tx.Exec("INSERT INTO "+mysqlDB+".orders(customer_id,amount,status,payload) VALUES (?,?,?,?)",
			1+rng.Intn(300), fmt.Sprintf("%d.%02d", rng.Intn(5000), rng.Intn(100)), statuses[rng.Intn(3)], payload)
		check(t, err, "写入 orders")
	}
	check(t, tx.Commit(), "提交 MySQL 种子数据")

	mysqlExec(t, d,
		"CREATE DATABASE "+mysqlBrokenDB+" CHARACTER SET utf8mb4",
		"CREATE TABLE "+mysqlBrokenDB+".a_items (id INT PRIMARY KEY, label VARCHAR(100) NOT NULL)",
		"CREATE TABLE "+mysqlBrokenDB+".gone (id INT PRIMARY KEY)",
		"CREATE VIEW "+mysqlBrokenDB+".v_broken AS SELECT id FROM "+mysqlBrokenDB+".gone",
		"DROP TABLE "+mysqlBrokenDB+".gone",
	)
	for i := 1; i <= 2000; i++ {
		_, err := d.Exec("INSERT INTO "+mysqlBrokenDB+".a_items VALUES (?, ?)", i, fmt.Sprintf("条目 %d %s", i, strings.Repeat("x", 40)))
		check(t, err, "写入 shop_broken.a_items")
	}
}

// restoreMySQL 把下载到的 .sql 用 mysql 客户端导入空白实例（导出带 CREATE DATABASE / USE，按原库名恢复）
func restoreMySQL(t testing.TB, dump []byte) {
	t.Helper()
	d := mysqlOpen(t, hostMySQLScratch, "")
	mysqlExec(t, d, "DROP DATABASE IF EXISTS "+mysqlDB)
	cmd := exec.Command("mysql", "--host="+hostMySQLScratch, "--port=3306", "--protocol=TCP", "--user=root", "--default-character-set=utf8mb4")
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+env(t, "IT_MYSQL_PASSWORD"))
	cmd.Stdin = bytes.NewReader(dump)
	out, err := cmd.CombinedOutput()
	check(t, err, "mysql 导入下载的导出：%s", out)
}

// mysqlContent 库的语义内容：每张表按主键排序的全部行（区分 NULL 与空串），以及视图、触发器、存储过程的定义
func mysqlContent(t testing.TB, host string) map[string][]string {
	t.Helper()
	d := mysqlOpen(t, host, mysqlDB)
	out := map[string][]string{}
	tables := queryStrings(t, d, "SELECT CONCAT(TABLE_NAME, ':', TABLE_TYPE) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", mysqlDB)
	out["tables"] = tables
	for _, tt := range tables {
		name, typ, _ := strings.Cut(tt, ":")
		if typ != "BASE TABLE" {
			continue
		}
		out["rows:"+name] = queryRows(t, d, "SELECT * FROM `"+name+"` ORDER BY 1")
	}
	out["views"] = queryStrings(t, d, "SELECT CONCAT(TABLE_NAME, ' ', VIEW_DEFINITION) FROM information_schema.VIEWS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", mysqlDB)
	out["triggers"] = queryStrings(t, d, "SELECT CONCAT(TRIGGER_NAME, ' ', EVENT_MANIPULATION, ' ', ACTION_TIMING, ' ', ACTION_STATEMENT) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? ORDER BY TRIGGER_NAME", mysqlDB)
	out["routines"] = queryStrings(t, d, "SELECT CONCAT(ROUTINE_NAME, ' ', ROUTINE_DEFINITION) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? ORDER BY ROUTINE_NAME", mysqlDB)
	return out
}

func queryStrings(t testing.TB, d *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := d.Query(q, args...)
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

// queryRows 每行编码成一个字符串：列之间用 0x1f 分隔，NULL 写成 \x00NULL，与空串区分
func queryRows(t testing.TB, d *sql.DB, q string) []string {
	t.Helper()
	rows, err := d.Query(q)
	check(t, err, "查询 %.80s", q)
	defer rows.Close()
	cols, err := rows.Columns()
	check(t, err, "读取列")
	var out []string
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		check(t, rows.Scan(ptrs...), "读取行")
		parts := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				parts[i] = "\x00NULL"
			} else {
				parts[i] = string(v)
			}
		}
		out = append(out, strings.Join(parts, "\x1f"))
	}
	check(t, rows.Err(), "遍历 %.80s", q)
	return out
}
