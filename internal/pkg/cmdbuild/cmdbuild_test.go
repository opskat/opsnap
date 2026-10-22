package cmdbuild

import (
	"strings"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试里用到的虚构凭据都带有明显的标记，便于在参数里搜索
//
//nolint:gosec // 测试用的虚构凭据
const (
	secRepo     = "SECRET-repo-pass"
	secNewKey   = "SECRET-new-key"
	secDB       = "SECRET-db-pass"
	secAWSID    = "SECRET-aws-id"
	secAWSKey   = "SECRET-aws-key"
	secREST     = "SECRET-rest-pass"
	secRclone   = "SECRET-rclone-obscured"
	secMongoPwd = `SECRET-mo"ngo'pa ss`
)

var allSecrets = []string{secRepo, secNewKey, secDB, secAWSID, secAWSKey, secREST, secRclone, secMongoPwd}

// assertNoSecretInArgs 断言任何凭据都不出现在程序名和参数里
func assertNoSecretInArgs(t *testing.T, c Cmd) {
	t.Helper()
	for _, s := range allSecrets {
		assert.NotContains(t, c.Name, s)
		for _, a := range c.Args {
			assert.NotContains(t, a, s, "参数 %q 含有凭据", a)
		}
		assert.NotContains(t, c.String(), s)
	}
}

// envValue 取出 Env 里某个变量的值
func envValue(c Cmd, key string) (string, bool) {
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// hasSeq 判断 args 中是否连续出现 seq
func hasSeq(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func localRepo() Repo {
	return Repo{Backend: LocalBackend{Path: "/nas/backup"}, Password: secRepo}
}

func TestCmdString(t *testing.T) {
	convey.Convey("命令行展示", t, func() {
		c := Cmd{Name: "restic", Args: []string{"backup", "/data/my dir", "it's"}, Env: []string{"RESTIC_PASSWORD=" + secRepo}, Stdin: []byte(secNewKey)}
		s := c.String()
		assert.Equal(t, `restic backup '/data/my dir' 'it'\''s'`, s)
		assert.NotContains(t, s, secRepo)
		assert.NotContains(t, s, secNewKey)
	})
}

func TestResticBackends(t *testing.T) {
	convey.Convey("restic 后端地址与凭据", t, func() {
		convey.Convey("本地目录", func() {
			c, err := ResticInit(localRepo())
			require.NoError(t, err)
			assert.Equal(t, "restic", c.Name)
			assert.Equal(t, []string{"init"}, c.Args)
			v, _ := envValue(c, "RESTIC_REPOSITORY")
			assert.Equal(t, "/nas/backup", v)
			v, _ = envValue(c, "RESTIC_PASSWORD")
			assert.Equal(t, secRepo, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("本地目录必须是绝对路径", func() {
			_, err := ResticInit(Repo{Backend: LocalBackend{Path: "nas"}, Password: secRepo})
			assert.Error(t, err)
		})
		convey.Convey("缺少仓库密码时报错", func() {
			_, err := ResticInit(Repo{Backend: LocalBackend{Path: "/nas"}})
			assert.Error(t, err)
		})
		convey.Convey("缺少后端时报错", func() {
			_, err := ResticInit(Repo{Password: secRepo})
			assert.Error(t, err)
		})
		convey.Convey("S3：密钥走 AWS 环境变量，路径风格走 -o", func() {
			c, err := ResticInit(Repo{Password: secRepo, Backend: S3Backend{
				Endpoint: "http://minio:9000", Bucket: "bk", Prefix: "opsnap/a", Region: "us-east-1",
				AccessKeyID: secAWSID, SecretAccessKey: secAWSKey, PathStyle: true,
			}})
			require.NoError(t, err)
			v, _ := envValue(c, "RESTIC_REPOSITORY")
			assert.Equal(t, "s3:http://minio:9000/bk/opsnap/a", v)
			v, _ = envValue(c, "AWS_ACCESS_KEY_ID")
			assert.Equal(t, secAWSID, v)
			v, _ = envValue(c, "AWS_SECRET_ACCESS_KEY")
			assert.Equal(t, secAWSKey, v)
			v, _ = envValue(c, "AWS_DEFAULT_REGION")
			assert.Equal(t, "us-east-1", v)
			assert.True(t, hasSeq(c.Args, "-o", "s3.bucket-lookup=path"))
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("S3 缺少 endpoint 或 bucket 时报错", func() {
			_, err := ResticInit(Repo{Password: secRepo, Backend: S3Backend{Bucket: "b"}})
			assert.Error(t, err)
			_, err = ResticInit(Repo{Password: secRepo, Backend: S3Backend{Endpoint: "https://s3"}})
			assert.Error(t, err)
		})
		convey.Convey("REST：用户名和密码走 RESTIC_REST_* 环境变量", func() {
			c, err := ResticInit(Repo{Password: secRepo, Backend: RESTBackend{URL: "https://nas:8000/opsnap/", User: "bk", Password: secREST}})
			require.NoError(t, err)
			v, _ := envValue(c, "RESTIC_REPOSITORY")
			assert.Equal(t, "rest:https://nas:8000/opsnap/", v)
			v, _ = envValue(c, "RESTIC_REST_USERNAME")
			assert.Equal(t, "bk", v)
			v, _ = envValue(c, "RESTIC_REST_PASSWORD")
			assert.Equal(t, secREST, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("REST 地址里带凭据时拒绝", func() {
			_, err := ResticInit(Repo{Password: secRepo, Backend: RESTBackend{URL: "https://u:p@nas:8000/"}}) //nolint:gosec // 测试拒绝内嵌凭据的地址
			assert.Error(t, err)
		})
		convey.Convey("WebDAV：经 rclone，配置全部走 RCLONE_CONFIG_* 环境变量", func() {
			c, err := ResticInit(Repo{Password: secRepo, Backend: WebDAVBackend{
				URL: "https://nas:5006/dav", Path: "opsnap", User: "bk", ObscuredPassword: secRclone, Vendor: "other",
			}})
			require.NoError(t, err)
			v, _ := envValue(c, "RESTIC_REPOSITORY")
			assert.Equal(t, "rclone:opsnap:opsnap", v)
			for key, want := range map[string]string{
				"RCLONE_CONFIG_OPSNAP_TYPE":   "webdav",
				"RCLONE_CONFIG_OPSNAP_URL":    "https://nas:5006/dav",
				"RCLONE_CONFIG_OPSNAP_VENDOR": "other",
				"RCLONE_CONFIG_OPSNAP_USER":   "bk",
				"RCLONE_CONFIG_OPSNAP_PASS":   secRclone,
			} {
				v, ok := envValue(c, key)
				assert.True(t, ok, key)
				assert.Equal(t, want, v, key)
			}
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("SFTP 用私钥：经 rclone，私钥文件路径与已知主机文件走环境变量", func() {
			c, err := ResticInit(Repo{Password: secRepo, Backend: SFTPBackend{
				Host: "nas", Port: 2222, User: "bk", Path: "/volume1/opsnap", KeyFile: "/run/opsnap/nas.key", KnownHostsFile: "/run/opsnap/known_hosts",
			}})
			require.NoError(t, err)
			v, _ := envValue(c, "RESTIC_REPOSITORY")
			assert.Equal(t, "rclone:opsnap:/volume1/opsnap", v)
			for key, want := range map[string]string{
				"RCLONE_CONFIG_OPSNAP_TYPE":             "sftp",
				"RCLONE_CONFIG_OPSNAP_HOST":             "nas",
				"RCLONE_CONFIG_OPSNAP_PORT":             "2222",
				"RCLONE_CONFIG_OPSNAP_USER":             "bk",
				"RCLONE_CONFIG_OPSNAP_KEY_FILE":         "/run/opsnap/nas.key",
				"RCLONE_CONFIG_OPSNAP_KNOWN_HOSTS_FILE": "/run/opsnap/known_hosts",
			} {
				v, ok := envValue(c, key)
				assert.True(t, ok, key)
				assert.Equal(t, want, v, key)
			}
			_, ok := envValue(c, "RCLONE_CONFIG_OPSNAP_PASS")
			assert.False(t, ok)
		})
		convey.Convey("SFTP 用密码：混淆后的密码走环境变量", func() {
			c, err := ResticInit(Repo{Password: secRepo, Backend: SFTPBackend{
				Host: "nas", User: "bk", Path: "/volume1/opsnap", ObscuredPassword: secRclone, KnownHostsFile: "/run/kh",
			}})
			require.NoError(t, err)
			v, _ := envValue(c, "RCLONE_CONFIG_OPSNAP_PASS")
			assert.Equal(t, secRclone, v)
			v, _ = envValue(c, "RCLONE_CONFIG_OPSNAP_PORT")
			assert.Equal(t, "22", v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("SFTP 缺少已知主机文件或认证方式时报错", func() {
			_, err := ResticInit(Repo{Password: secRepo, Backend: SFTPBackend{Host: "nas", User: "bk", Path: "/p", KeyFile: "/k"}})
			assert.Error(t, err)
			_, err = ResticInit(Repo{Password: secRepo, Backend: SFTPBackend{Host: "nas", User: "bk", Path: "/p", KnownHostsFile: "/kh"}})
			assert.Error(t, err)
		})
		convey.Convey("rclone 混淆密码：明文经标准输入传入", func() {
			c, err := RcloneObscure(secREST)
			require.NoError(t, err)
			assert.Equal(t, "rclone", c.Name)
			assert.Equal(t, []string{"obscure", "-"}, c.Args)
			assert.Equal(t, []byte(secREST), c.Stdin)
			assertNoSecretInArgs(t, c)
			_, err = RcloneObscure("")
			assert.Error(t, err)
		})
		convey.Convey("rclone about 查询 SFTP/WebDAV 的剩余空间：配置与混淆密码只走环境变量", func() {
			c, err := RcloneAbout(SFTPBackend{
				Host: "nas", User: "bk", Path: "/volume1/opsnap", ObscuredPassword: secRclone, KnownHostsFile: "/run/kh",
			})
			require.NoError(t, err)
			assert.Equal(t, "rclone", c.Name)
			assert.Equal(t, []string{"about", "opsnap:/volume1/opsnap", "--json"}, c.Args)
			v, _ := envValue(c, "RCLONE_CONFIG_OPSNAP_PASS")
			assert.Equal(t, secRclone, v)
			assertNoSecretInArgs(t, c)

			c, err = RcloneAbout(WebDAVBackend{URL: "https://nas:5006/dav", Path: "opsnap", User: "bk", ObscuredPassword: secRclone})
			require.NoError(t, err)
			assert.Equal(t, []string{"about", "opsnap:opsnap", "--json"}, c.Args)
			assertNoSecretInArgs(t, c)

			_, err = RcloneAbout(S3Backend{Endpoint: "https://s3", Bucket: "b"})
			assert.Error(t, err, "不经 rclone 的后端没有 about")
		})
		convey.Convey("压缩与限速作为全局参数", func() {
			r := localRepo()
			r.Compression = CompressionMax
			r.LimitUploadKiB = 2048
			c, err := ResticPrune(r)
			require.NoError(t, err)
			assert.Equal(t, "prune", c.Args[0])
			assert.True(t, hasSeq(c.Args, "--compression", "max"))
			assert.True(t, hasSeq(c.Args, "--limit-upload", "2048"))
		})
		convey.Convey("未知的压缩方式报错", func() {
			r := localRepo()
			r.Compression = "zstd"
			_, err := ResticPrune(r)
			assert.Error(t, err)
		})
	})
}

func TestResticCommands(t *testing.T) {
	convey.Convey("restic 子命令", t, func() {
		repo := localRepo()

		convey.Convey("导出命令经 --stdin-from-command 交给 restic 运行，导出凭据并入环境变量", func() {
			dump, err := Dump(DumpSpec{Kind: DBPostgres, Conn: DBConn{Host: "127.0.0.1", Port: 15432, User: "pg", Password: secDB}, Databases: []string{"app"}})
			require.NoError(t, err)
			c, err := ResticBackupStdin(repo, StdinBackup{Filename: "postgres/app.dump", Tags: []string{"opsnap-task-3"}, Command: dump})
			require.NoError(t, err)
			assert.Equal(t, "restic", c.Name)
			assert.Equal(t, "backup", c.Args[0])
			assert.True(t, hasSeq(c.Args, "--stdin-from-command"))
			assert.True(t, hasSeq(c.Args, "--stdin-filename", "postgres/app.dump"))
			assert.True(t, hasSeq(c.Args, "--tag", "opsnap-task-3"))
			assert.True(t, hasSeq(c.Args, "--host", DefaultHost))
			assert.True(t, hasSeq(c.Args, "--json"))
			// "--" 之后正好是导出命令本身
			assert.True(t, hasSeq(c.Args, append([]string{"--", dump.Name}, dump.Args...)...))
			assert.Equal(t, append([]string{"--", dump.Name}, dump.Args...), c.Args[len(c.Args)-len(dump.Args)-2:])
			v, _ := envValue(c, "PGPASSWORD")
			assert.Equal(t, secDB, v)
			v, _ = envValue(c, "RESTIC_PASSWORD")
			assert.Equal(t, secRepo, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("stdin 备份缺少文件名、命令，或命令需要标准输入时报错", func() {
			_, err := ResticBackupStdin(repo, StdinBackup{Command: Cmd{Name: "pg_dump"}})
			assert.Error(t, err)
			_, err = ResticBackupStdin(repo, StdinBackup{Filename: "a"})
			assert.Error(t, err)
			_, err = ResticBackupStdin(repo, StdinBackup{Filename: "a", Command: Cmd{Name: "x", Stdin: []byte("y")}})
			assert.Error(t, err)
		})
		convey.Convey("镜像目录备份", func() {
			c, err := ResticBackupDir(repo, DirBackup{Path: "/data/mirror/task-3", Tags: []string{"opsnap-task-3"}, Host: "h1"})
			require.NoError(t, err)
			assert.Equal(t, "backup", c.Args[0])
			assert.True(t, hasSeq(c.Args, "--host", "h1"))
			assert.True(t, hasSeq(c.Args, "--tag", "opsnap-task-3"))
			assert.Equal(t, "/data/mirror/task-3", c.Args[len(c.Args)-1])
			assert.False(t, hasSeq(c.Args, "--stdin-from-command"))
			_, err = ResticBackupDir(repo, DirBackup{Path: "mirror"})
			assert.Error(t, err)
		})
		convey.Convey("forget 只作用于本任务的快照并带上保留参数", func() {
			c, err := ResticForget(repo, ForgetSpec{Tags: []string{"opsnap-task-3"}, Policy: []string{"--keep-daily", "7"}})
			require.NoError(t, err)
			assert.Equal(t, "forget", c.Args[0])
			assert.True(t, hasSeq(c.Args, "--tag", "opsnap-task-3"))
			assert.True(t, hasSeq(c.Args, "--keep-daily", "7"))
			assert.False(t, hasSeq(c.Args, "--prune"))
			c, err = ResticForget(repo, ForgetSpec{Tags: []string{"a", "b"}, Policy: []string{"--keep-last", "1"}, Prune: true})
			require.NoError(t, err)
			assert.True(t, hasSeq(c.Args, "--tag", "a,b"))
			assert.True(t, hasSeq(c.Args, "--prune"))
		})
		convey.Convey("forget 没有标签或保留参数时拒绝，避免波及其他任务或删光快照", func() {
			_, err := ResticForget(repo, ForgetSpec{Policy: []string{"--keep-last", "1"}})
			assert.Error(t, err)
			_, err = ResticForget(repo, ForgetSpec{Tags: []string{"t"}})
			assert.Error(t, err)
		})
		convey.Convey("查询与维护命令", func() {
			cases := []struct {
				name string
				fn   func() (Cmd, error)
				want []string
			}{
				{"snapshots", func() (Cmd, error) { return ResticSnapshots(repo, []string{"opsnap-task-3"}) }, []string{"snapshots", "--json", "--tag", "opsnap-task-3"}},
				{"snapshots 全部", func() (Cmd, error) { return ResticSnapshots(repo, nil) }, []string{"snapshots", "--json"}},
				{"ls", func() (Cmd, error) { return ResticLs(repo, "abc123", "/mysql") }, []string{"ls", "--json", "abc123", "/mysql"}},
				{"dump 单个文件", func() (Cmd, error) { return ResticDump(repo, "abc123", "/mysql/databases.sql", "") }, []string{"dump", "abc123", "/mysql/databases.sql"}},
				{"dump 目录打包", func() (Cmd, error) { return ResticDump(repo, "latest", "/data", "zip") }, []string{"dump", "--archive", "zip", "latest", "/data"}},
				{"stats", func() (Cmd, error) { return ResticStats(repo, "raw-data") }, []string{"stats", "--json", "--mode", "raw-data"}},
				{"check 抽样", func() (Cmd, error) { return ResticCheck(repo, "5%") }, []string{"check", "--read-data-subset", "5%"}},
				{"check", func() (Cmd, error) { return ResticCheck(repo, "") }, []string{"check"}},
				{"prune", func() (Cmd, error) { return ResticPrune(repo) }, []string{"prune"}},
				{"unlock", func() (Cmd, error) { return ResticUnlock(repo, false) }, []string{"unlock"}},
				{"unlock 全部", func() (Cmd, error) { return ResticUnlock(repo, true) }, []string{"unlock", "--remove-all"}},
				{"key list", func() (Cmd, error) { return ResticKeyList(repo) }, []string{"key", "list", "--json"}},
				{"key remove", func() (Cmd, error) { return ResticKeyRemove(repo, "5f3a9c") }, []string{"key", "remove", "5f3a9c"}},
			}
			for _, tc := range cases {
				c, err := tc.fn()
				require.NoError(t, err, tc.name)
				assert.Equal(t, "restic", c.Name, tc.name)
				assert.Equal(t, tc.want, c.Args, tc.name)
				assertNoSecretInArgs(t, c)
			}
		})
		convey.Convey("快照 ID、密钥 ID 不能是空的或像参数", func() {
			_, err := ResticLs(repo, "", "")
			assert.Error(t, err)
			_, err = ResticDump(repo, "--help", "/x", "")
			assert.Error(t, err)
			_, err = ResticDump(repo, "abc", "", "")
			assert.Error(t, err)
			_, err = ResticDump(repo, "abc", "/x", "rar")
			assert.Error(t, err)
			_, err = ResticKeyRemove(repo, "-x")
			assert.Error(t, err)
		})
		convey.Convey("key add：新密钥经标准输入传入", func() {
			c, err := ResticKeyAdd(repo, secNewKey)
			require.NoError(t, err)
			assert.Equal(t, []string{"key", "add", "--new-password-file", "/dev/stdin"}, c.Args)
			assert.Equal(t, []byte(secNewKey), c.Stdin)
			assertNoSecretInArgs(t, c)
			for _, kv := range c.Env {
				assert.NotContains(t, kv, secNewKey)
			}
			_, err = ResticKeyAdd(repo, "")
			assert.Error(t, err)
		})
	})
}

func TestDump(t *testing.T) {
	convey.Convey("各数据库的导出命令", t, func() {
		direct := DBConn{Host: "10.0.0.8", Port: 3306, User: "bk", Password: secDB}
		viaSSH := DBConn{Host: "127.0.0.1", Port: 40001, User: "bk", Password: secDB}

		convey.Convey("MySQL：一致性快照，密码走 MYSQL_PWD", func() {
			c, err := Dump(DumpSpec{Kind: DBMySQL, Conn: direct, Databases: []string{"app", "crm"}})
			require.NoError(t, err)
			assert.Equal(t, "mysqldump", c.Name)
			for _, a := range []string{"--host=10.0.0.8", "--port=3306", "--user=bk", "--protocol=TCP", "--single-transaction", "--quick", "--routines", "--hex-blob"} {
				assert.Contains(t, c.Args, a)
			}
			assert.True(t, hasSeq(c.Args, "--databases", "app", "crm"))
			v, _ := envValue(c, "MYSQL_PWD")
			assert.Equal(t, secDB, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("MySQL 全部库用 --all-databases，密码走 MYSQL_PWD", func() {
			c, err := Dump(DumpSpec{Kind: DBMySQL, Conn: direct, AllDatabases: true})
			require.NoError(t, err)
			assert.Equal(t, "mysqldump", c.Name)
			assert.Contains(t, c.Args, "--all-databases")
			assert.NotContains(t, c.Args, "--databases")
			v, _ := envValue(c, "MYSQL_PWD")
			assert.Equal(t, secDB, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("PostgreSQL：自定义格式、不压缩、不提示密码，密码走 PGPASSWORD", func() {
			c, err := Dump(DumpSpec{Kind: DBPostgres, Conn: DBConn{Host: "db", Port: 5432, User: "pg", Password: secDB}, Databases: []string{"app"}})
			require.NoError(t, err)
			assert.Equal(t, "pg_dump", c.Name)
			for _, a := range []string{"--host=db", "--port=5432", "--username=pg", "--no-password", "--format=custom", "--compress=0", "--dbname=app"} {
				assert.Contains(t, c.Args, a)
			}
			v, _ := envValue(c, "PGPASSWORD")
			assert.Equal(t, secDB, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("PostgreSQL 每次只导出一个库，库名不能是连接串", func() {
			_, err := Dump(DumpSpec{Kind: DBPostgres, Conn: direct, Databases: []string{"a", "b"}})
			assert.Error(t, err)
			_, err = Dump(DumpSpec{Kind: DBPostgres, Conn: direct, AllDatabases: true})
			assert.Error(t, err)
			_, err = Dump(DumpSpec{Kind: DBPostgres, Conn: direct, Databases: []string{"host=evil password=x"}})
			assert.Error(t, err)
			_, err = Dump(DumpSpec{Kind: DBPostgres, Conn: direct, Databases: []string{"postgresql://u:p@h/db"}})
			assert.Error(t, err)
		})
		convey.Convey("MongoDB：archive 格式，密码经环境变量写成配置文件从标准输入交给 --config", func() {
			c, err := Dump(DumpSpec{Kind: DBMongo, Conn: DBConn{Host: "mongo", Port: 27017, User: "root", Password: secMongoPwd}, Databases: []string{"app"}})
			require.NoError(t, err)
			assert.Equal(t, "sh", c.Name)
			require.GreaterOrEqual(t, len(c.Args), 3)
			assert.Equal(t, "-c", c.Args[0])
			assert.Contains(t, c.Args[1], "mongodump --config=/dev/stdin")
			assert.Contains(t, c.Args[1], `"$`+mongoConfigEnv+`"`)
			for _, a := range []string{"--host=mongo", "--port=27017", "--username=root", "--authenticationDatabase=admin", "--archive", "--db=app"} {
				assert.Contains(t, c.Args, a)
			}
			cfg, ok := envValue(c, mongoConfigEnv)
			require.True(t, ok)
			assert.Equal(t, `password: "SECRET-mo\"ngo'pa ss"`+"\n", cfg)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("MongoDB 全部库不带 --db，自定义认证库生效", func() {
			c, err := Dump(DumpSpec{Kind: DBMongo, Conn: DBConn{Host: "mongo", Port: 27017, User: "root", Password: secDB}, AllDatabases: true, AuthDatabase: "users"})
			require.NoError(t, err)
			for _, a := range c.Args {
				assert.False(t, strings.HasPrefix(a, "--db="))
			}
			assert.Contains(t, c.Args, "--authenticationDatabase=users")
			_, err = Dump(DumpSpec{Kind: DBMongo, Conn: direct, Databases: []string{"a", "b"}})
			assert.Error(t, err)
		})
		convey.Convey("MongoDB 没有用户时直接运行 mongodump，不带认证参数", func() {
			c, err := Dump(DumpSpec{Kind: DBMongo, Conn: DBConn{Host: "mongo", Port: 27017}, AllDatabases: true})
			require.NoError(t, err)
			assert.Equal(t, "mongodump", c.Name)
			assert.Equal(t, []string{"--host=mongo", "--port=27017", "--archive"}, c.Args)
			assert.Empty(t, c.Env)
		})
		convey.Convey("Redis：取 RDB 到标准输出，密码走 REDISCLI_AUTH", func() {
			c, err := Dump(DumpSpec{Kind: DBRedis, Conn: DBConn{Host: "redis", Port: 6379, User: "default", Password: secDB}})
			require.NoError(t, err)
			assert.Equal(t, "redis-cli", c.Name)
			assert.Equal(t, []string{"-h", "redis", "-p", "6379", "--user", "default", "--rdb", "-"}, c.Args)
			v, _ := envValue(c, "REDISCLI_AUTH")
			assert.Equal(t, secDB, v)
			assertNoSecretInArgs(t, c)
		})
		convey.Convey("附加参数追加在末尾，库名不能像参数", func() {
			c, err := Dump(DumpSpec{Kind: DBMySQL, Conn: direct, AllDatabases: true, ExtraArgs: []string{"--skip-extended-insert"}})
			require.NoError(t, err)
			assert.Equal(t, "--skip-extended-insert", c.Args[len(c.Args)-1])
			_, err = Dump(DumpSpec{Kind: DBMySQL, Conn: direct, Databases: []string{"--all-databases"}})
			assert.Error(t, err)
		})
		convey.Convey("缺少库、地址或类型未知时报错", func() {
			_, err := Dump(DumpSpec{Kind: DBMySQL, Conn: direct})
			assert.Error(t, err)
			_, err = Dump(DumpSpec{Kind: DBMySQL, Conn: DBConn{Port: 3306}, AllDatabases: true})
			assert.Error(t, err)
			_, err = Dump(DumpSpec{Kind: DBMySQL, Conn: DBConn{Host: "h"}, AllDatabases: true})
			assert.Error(t, err)
			_, err = Dump(DumpSpec{Kind: "oracle", Conn: direct, AllDatabases: true})
			assert.Error(t, err)
		})
		convey.Convey("经 SSH 与直连只差地址和端口", func() {
			specs := []DumpSpec{
				{Kind: DBMySQL, Databases: []string{"app"}},
				{Kind: DBPostgres, Databases: []string{"app"}},
				{Kind: DBMongo, Databases: []string{"app"}},
				{Kind: DBRedis},
			}
			for _, s := range specs {
				s.Conn = direct
				a, err := Dump(s)
				require.NoError(t, err, s.Kind)
				s.Conn = viaSSH
				b, err := Dump(s)
				require.NoError(t, err, s.Kind)
				assert.Equal(t, a.Name, b.Name)
				assert.Equal(t, a.Env, b.Env)
				repl := strings.NewReplacer("10.0.0.8", "127.0.0.1", "3306", "40001")
				want := make([]string, len(a.Args))
				for i, x := range a.Args {
					want[i] = repl.Replace(x)
				}
				assert.Equal(t, want, b.Args, s.Kind)
				assert.NotEqual(t, a.Args, b.Args, s.Kind)
				assertNoSecretInArgs(t, a)
				assertNoSecretInArgs(t, b)
			}
		})
	})
}

func TestDumpFilename(t *testing.T) {
	convey.Convey("快照里的导出文件名", t, func() {
		cases := []struct {
			spec DumpSpec
			want string
		}{
			{DumpSpec{Kind: DBMySQL, Databases: []string{"a", "b"}}, "mysql/databases.sql"},
			{DumpSpec{Kind: DBMySQL, AllDatabases: true}, "mysql/databases.sql"},
			{DumpSpec{Kind: DBPostgres, Databases: []string{"app/x"}}, "postgres/app_x.dump"},
			{DumpSpec{Kind: DBMongo, Databases: []string{"app"}}, "mongodb/app.archive"},
			{DumpSpec{Kind: DBMongo, AllDatabases: true}, "mongodb/all.archive"},
			{DumpSpec{Kind: DBRedis}, "redis/dump.rdb"},
		}
		for _, tc := range cases {
			assert.Equal(t, tc.want, tc.spec.Filename())
		}
	})
}

func TestResticForgetDryRun(t *testing.T) {
	r := Repo{Backend: LocalBackend{Path: "/repo"}, Password: "pw"}
	c, err := ResticForget(r, ForgetSpec{Tags: []string{"job:3"}, Policy: []string{"--keep-daily", "7"}, DryRun: true})
	require.NoError(t, err)
	assert.Contains(t, c.Args, "--dry-run")
	assert.Contains(t, c.Args, "--no-lock", "只演算时不取 restic 的独占锁，不挡住同一仓库上的备份")
	assert.NotContains(t, c.Args, "--prune")
	c, err = ResticForget(r, ForgetSpec{Tags: []string{"job:3"}, Policy: []string{"--keep-daily", "7"}})
	require.NoError(t, err)
	assert.NotContains(t, c.Args, "--dry-run")
	assert.NotContains(t, c.Args, "--no-lock", "真正删除时必须加锁")
}

// TestResticForgetRetryLock 撞锁时等一会儿再放弃：只有真正删除时才带重试，只演算时不带
func TestResticForgetRetryLock(t *testing.T) {
	r := Repo{Backend: LocalBackend{Path: "/repo"}, Password: "pw"}

	// 不给重试时长：与现在一致，不加参数
	c, err := ResticForget(r, ForgetSpec{Tags: []string{"job:3"}, Policy: []string{"--keep-daily", "7"}})
	require.NoError(t, err)
	assert.NotContains(t, c.Args, "--retry-lock")

	// 给了重试时长：带上 restic 认识的时长写法
	c, err = ResticForget(r, ForgetSpec{
		Tags: []string{"job:3"}, Policy: []string{"--keep-daily", "7"}, RetryLock: 5 * time.Minute,
	})
	require.NoError(t, err)
	assert.True(t, hasSeq(c.Args, "--retry-lock", "5m0s"))

	// 只演算：不取锁，也就没有重试
	c, err = ResticForget(r, ForgetSpec{
		Tags: []string{"job:3"}, Policy: []string{"--keep-daily", "7"}, DryRun: true, RetryLock: 5 * time.Minute,
	})
	require.NoError(t, err)
	assert.NotContains(t, c.Args, "--retry-lock", "只演算时不取锁")
	assert.Contains(t, c.Args, "--no-lock")
}

func TestCmdSecrets(t *testing.T) {
	c := Cmd{Env: []string{
		"RESTIC_REPOSITORY=rclone:opsnap:/backup", "RESTIC_PASSWORD=SECRET-repo",
		"AWS_ACCESS_KEY_ID=AKID", "AWS_SECRET_ACCESS_KEY=SECRET-s3",
		"RCLONE_CONFIG_OPSNAP_PORT=22", "RCLONE_CONFIG_OPSNAP_PASS=SECRET-obscured",
		"MYSQL_PWD=SECRET-mysql", "PGPASSWORD=SECRET-pg", "REDISCLI_AUTH=SECRET-redis",
		mongoConfigEnv + "=password: SECRET-mongo", "OPSNAP_RSH_TOKEN=SECRET-token",
		"RESTIC_PROGRESS_FPS=1", "RESTIC_REST_PASSWORD=",
	}}
	assert.ElementsMatch(t, []string{
		"SECRET-repo", "SECRET-s3", "SECRET-obscured", "SECRET-mysql", "SECRET-pg", "SECRET-redis",
		"password: SECRET-mongo", "SECRET-token",
	}, c.Secrets())
}
