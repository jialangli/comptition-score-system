package postgres

import (
	"context"
	"strings"
)

// TruncateAll 清空全部业务表，并重置自增序列。
//
// 用途有两个：集成测试的用例间隔离，以及本地「把演示数据清干净重来」。
//
// 为什么集中在这里而不是让测试自己拼 SQL：TRUNCATE 的表清单必须与
// 迁移脚本保持同步。散落在测试里的表名清单，早晚会在新增表之后漏掉一张，
// 于是测试之间开始互相污染，而且症状是「单独跑能过、全量跑就挂」——
// 最难排查的那类问题。新增表时请一并更新本清单。
func (d *DB) TruncateAll(ctx context.Context) error {
	// 顺序无实际影响（CASCADE），但按「先叶子后根」排列便于人工核对
	tables := []string{
		"slot_snapshots",
		"slot_teams",
		"slots",
		"seats",
		"scores",
		"teams",
		"tasks",
		"screen_config",
		"config_snapshots",
		"import_logs",
		"audit_logs",
		"events",
	}
	_, err := d.pool.Exec(ctx,
		"TRUNCATE TABLE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE")
	return mapError(err)
}
