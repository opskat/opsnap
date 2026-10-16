package snapshot_svc

import (
	"strings"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
)

// restoreCommand 恢复命令示例。file 是下载得到的文件名（不含 .gz），gz 表示下载时压缩过。
// 地址、账号、库名需要按目标环境替换；示例里不放任何凭据，密码由工具交互式询问或读环境变量。
// opsnap 自己不会写回任何线上数据库。
func restoreCommand(source, file string, gz bool) string {
	downloaded := file
	if gz {
		downloaded += ".gz"
	}
	// feed 把下载文件接到恢复工具的标准输入：压缩过的先解压
	feed := func(tool string) string {
		if gz {
			return "gunzip < " + downloaded + " | " + tool
		}
		return tool + " < " + file
	}
	switch source {
	case job_entity.SourceMySQL, job_entity.SourceMariaDB:
		// 导出时用了 --databases，文件里自带 CREATE DATABASE 与 USE，不需要指定库
		tool := "mysql"
		if source == job_entity.SourceMariaDB {
			tool = "mariadb"
		}
		if gz {
			return "gunzip < " + downloaded + " | " + tool + " -h 127.0.0.1 -u root -p"
		}
		return tool + " -h 127.0.0.1 -u root -p < " + file
	case job_entity.SourcePostgres:
		const opts = "-h 127.0.0.1 -U postgres --clean --if-exists --dbname=目标库名"
		if gz {
			return feed("pg_restore " + opts)
		}
		return "pg_restore " + opts + " " + file
	case job_entity.SourceMongoDB:
		if gz {
			return feed("mongorestore --uri=\"mongodb://127.0.0.1:27017\" --archive")
		}
		return "mongorestore --uri=\"mongodb://127.0.0.1:27017\" --archive=" + file
	case job_entity.SourceRedis:
		return strings.Join([]string{
			"# 1. 停止目标 Redis；开启了 AOF 的先在配置里设 appendonly no",
			"redis-cli CONFIG GET dir      # 在停止前记下数据目录",
			"# 2. 用 " + file + " 替换数据目录里的 dump.rdb（文件属主与原文件一致）",
			"cp " + file + " /var/lib/redis/dump.rdb",
			"# 3. 启动 redis-server，数据即恢复",
		}, "\n")
	}
	return ""
}
