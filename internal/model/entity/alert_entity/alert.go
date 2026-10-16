// Package alert_entity 告警状态：记住已经发出过「太久没有成功」的任务，重启后不重发
package alert_entity

// StaleAlert 一个任务正处于「太久没有成功」的告警中。Since 是 Unix 秒，为发出告警的时间。
// 任务恢复成功时删除这一行并发出「恢复正常」。
type StaleAlert struct {
	JobID      int64 `gorm:"column:job_id;primaryKey;autoIncrement:false"`
	Since      int64 `gorm:"column:since;not null"`
	Createtime int64 `gorm:"column:createtime"`
}

func (StaleAlert) TableName() string { return "stale_alerts" }
