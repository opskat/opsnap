// Package masterkey_entity 主密钥校验值：判断当前主密钥是否就是加密现有凭据的那把
package masterkey_entity

const (
	// SingletonID 只有一行，主键固定为 1
	SingletonID = 1
	// CheckPlain 校验值的明文；用主密钥加密后保存，解得开且内容一致就是同一把主密钥
	CheckPlain = "opsnap master key check v1"
)

// MasterKey 主密钥校验值。RotatedAt 是最近一次轮换的时间（Unix 秒），没有轮换过为 0。
type MasterKey struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement:false"`
	CheckValue string `gorm:"column:check_value;not null"`
	RotatedAt  int64  `gorm:"column:rotated_at;not null"`
	Createtime int64  `gorm:"column:createtime"`
}

func (MasterKey) TableName() string { return "master_key" }
