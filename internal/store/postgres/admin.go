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
	// 0004 新增的两张表也要清空：
	//   score_change_requests —— 改分申请单，漏了会让用例间互相污染
	//   disputes             —— 争议工单（0005）。同样是队伍级叶子表，
	//                            且带部分唯一索引，残留会让「重复上报」类断言随机失败
	//   release_units        —— 发布单元（0006）
	//   referee_codes        —— 裁判码（0007）。带唯一索引，残留会让建档类用例撞重复
	//   team_write_locks     —— 赛台-队伍可写锁（0008）。残留会让「抢锁」类用例
	//                            一开始就被别人占着，症状是随机失败
	//   evidence             —— 留底证据库（0009）。带唯一索引，同样会撞重复
	//   contests             —— 赛事表。放在最后（其余表外键引用它，
	//                            CASCADE 会连带清掉子表，顺序上仍是「先叶子后根」）
	tables := []string{
		"slot_snapshots",
		"slot_teams",
		"slots",
		"seats",
		"scores",
		"score_change_requests",
		"disputes",
		"release_units",
		"referee_codes",
		"team_write_locks",
		"evidence",
		"teams",
		"tasks",
		"screen_config",
		"config_snapshots",
		"import_logs",
		"audit_logs",
		"events",
		"contests",
	}
	if _, err := d.pool.Exec(ctx,
		"TRUNCATE TABLE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
		return mapError(err)
	}

	// ⚠️ 清空后必须把默认赛事补回来。
	// 各表外键都指向 contests(id)，而 ct_default 是「未指定赛事」的兜底；
	// 它被 TRUNCATE 掉之后，任何不带赛事标识的写入都会撞 fk_*_contest。
	// 症状是「单独跑能过、全量跑就挂」—— 因为前一个用例把 contests 清了，
	// 后一个用例还以为默认赛事在。
	return (&ContestStore{q: d.pool}).EnsureDefault(ctx)
}
