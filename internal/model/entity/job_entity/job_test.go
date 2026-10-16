package job_entity

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDBAddr(t *testing.T) {
	for _, c := range []struct{ host, want string }{
		{"10.0.0.5", "10.0.0.5:3306"},
		{"db.internal", "db.internal:3306"},
		// IPv6 地址要加方括号，否则端口和地址分不开，转发、探测和导出都会失败
		{"fd00::5", "[fd00::5]:3306"},
	} {
		j := &Job{DBHost: c.host, DBPort: 3306}
		assert.Equal(t, c.want, j.DBAddr(), c.host)
		host, port, err := net.SplitHostPort(j.DBAddr())
		require.NoError(t, err, c.host)
		assert.Equal(t, c.host, host)
		assert.Equal(t, "3306", port)
	}
}

// 保存时就拒绝导出命令在运行时才会拒绝的值，否则任务能保存却每次运行都失败
func TestValidateRejectsWhatDumpRejects(t *testing.T) {
	base := func() *Job {
		return &Job{
			Name: "db", SourceType: SourcePostgres, ConnMode: ConnDirect,
			DBHost: "10.0.0.5", DBPort: 5432, DBUser: "backup", Databases: []string{"app"},
		}
	}
	fields := func(j *Job) []string {
		errs := j.ValidateConfig()
		out := make([]string, 0, len(errs))
		for _, e := range errs {
			out = append(out, e.Field)
		}
		return out
	}
	require.NotContains(t, fields(base()), "databases")
	require.NotContains(t, fields(base()), "db_host")

	j := base()
	j.DBHost = "-oProxyCommand=x"
	assert.Contains(t, fields(j), "db_host")
	for _, db := range []string{"-v", "dbname=other host=evil", "postgresql://evil/app"} {
		j := base()
		j.Databases = []string{db}
		assert.Contains(t, fields(j), "databases", db)
	}
	j = base()
	j.SourceType = SourceMySQL
	j.DBPort = 3306
	j.Databases = []string{"--all-databases"}
	assert.Contains(t, fields(j), "databases")
}
