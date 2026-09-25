package probe

// itemTitles 每个探测项的标题，键为 Item.Key；供各 decide* 函数与查询失败时的兜底结果共用
var itemTitles = map[string]Text{
	"mysql.version":                {ZhCN: "版本", En: "Version"},
	"mysql.binlog":                 {ZhCN: "binlog", En: "Binlog"},
	"mysql.binlog_format":          {ZhCN: "binlog 格式", En: "Binlog format"},
	"mysql.gtid":                   {ZhCN: "GTID", En: "GTID"},
	"mysql.binlog_retention":       {ZhCN: "binlog 保留时长", En: "Binlog retention"},
	"mysql.replication_privileges": {ZhCN: "复制权限", En: "Replication privileges"},
	"mysql.non_innodb_tables":      {ZhCN: "非 InnoDB 表", En: "Non-InnoDB tables"},
	"mysql.mysqldump":              {ZhCN: "主控端 mysqldump", En: "mysqldump on the control host"},
	"postgres.version":             {ZhCN: "版本", En: "Version"},
	"postgres.wal_level":           {ZhCN: "wal_level", En: "wal_level"},
	"postgres.max_wal_senders":     {ZhCN: "max_wal_senders", En: "max_wal_senders"},
	"postgres.replication_slots":   {ZhCN: "复制槽余量", En: "Replication slot margin"},
	"postgres.replication_attr":    {ZhCN: "REPLICATION 属性", En: "REPLICATION attribute"},
	"postgres.pg_dump":             {ZhCN: "主控端 pg_dump", En: "pg_dump on the control host"},
	"server_file.ssh_reachable":    {ZhCN: "SSH 可达", En: "SSH reachable"},
	"server_file.cpu_arch":         {ZhCN: "CPU 架构", En: "CPU architecture"},
	"server_file.tmp_exec":         {ZhCN: "临时目录可执行", En: "Temp directory executable"},
	"server_file.sudo":             {ZhCN: "免密 sudo", En: "Passwordless sudo"},
}

// queryErrorItem 某个只读查询本身失败时的兜底结果：连接已经建立成功（否则不会走到这一步），
// 但这一项依赖的查询出错（例如权限不足），据实标记为不可用，而不是冒充“无法探测”（那是整次连接失败时的状态，不在本包）
func queryErrorItem(key string, err error) Item {
	return Item{
		Key:   key,
		Title: itemTitles[key],
		Tier:  TierFail,
		Detail: Text{
			ZhCN: "查询失败：" + err.Error(),
			En:   "Query failed: " + err.Error(),
		},
	}
}
