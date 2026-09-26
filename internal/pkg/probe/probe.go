// Package probe 数据源保存后的能力探测：按数据源类型（dsconn.Type）注册一组探测项，
// 在已打开的 dsconn.Conn 上只读地判断每一项的可用性，并给出可直接复制的修复方法。
// 探测过程只读，唯一的例外是“临时目录能否执行程序”一项，它会写入一个临时文件并在检查后删除（见 serverfile.go）。
// 探测项文案（标题、详情、修复方法）以中英文两种语言直接返回，供前端按当前语言展示；
// Item.Key 是稳定标识，供前端或将来的持久化按项对齐历史结果。
package probe

import (
	"context"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// Tier 探测项的判定档位
type Tier string

const (
	// TierOK 可用
	TierOK Tier = "ok"
	// TierWarn 可用但有风险
	TierWarn Tier = "warn"
	// TierFail 不可用
	TierFail Tier = "fail"
)

// Text 一段中英文文案
type Text struct {
	ZhCN string
	En   string
}

// Item 一个探测项的结果
type Item struct {
	// Key 稳定标识，如 "mysql.binlog"
	Key   string
	Title Text
	Tier  Tier
	// Detail 实际读到的值与说明
	Detail Text
	// Fix 可直接复制的修复方法；Tier 为 TierOK，或该项没有修复方法时为空 Text
	Fix Text
	// Tables 非 InnoDB 表等项列出的表名（"库.表"），最多 5 个
	Tables []string
	// TableCount Tables 对应的总数，可能大于 len(Tables)
	TableCount int
}

// Func 某个数据源类型的探测实现，ctx 受调用方约束（服务侧的整体超时不在本包）
type Func func(ctx context.Context, conn *dsconn.Conn) []Item

var registry = map[dsconn.Type]Func{}

// Register 注册某个数据源类型的探测项；用于扩展新的数据源类型
func Register(typ dsconn.Type, fn Func) { registry[typ] = fn }

func init() {
	Register(dsconn.TypeMySQL, mysqlItems)
	Register(dsconn.TypePostgreSQL, postgresItems)
	Register(dsconn.TypeServerFile, serverFileItems)
}

// Run 对已打开的连接按数据源类型执行探测，按固定顺序返回探测项；未注册的类型返回 nil。
// conn 必须已经连接成功（连接失败时的“无法探测”由调用方在 dsconn.Open 失败处处理，不在本包）。
func Run(ctx context.Context, typ dsconn.Type, conn *dsconn.Conn) []Item {
	fn, ok := registry[typ]
	if !ok {
		return nil
	}
	return fn(ctx, conn)
}
