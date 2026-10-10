package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 队伍级归台 + 参赛轮次（迁移 0016）
//
// 这两列是前端原型「分台与顺位」的事实源：后端此前既无 seat_id 也无 session，
// 「按队伍参赛轮次判」这类判据在后端**无法表达**。本文件守住四件事：
//
//  1. 归台 / 取消归台真的落库（断言一律读库，不信返回值 —— 返回值是「顺便更新」的）；
//  2. 删赛台时队伍回到「未排台」而不是被拦住（0016 刻意用 ON DELETE SET NULL，
//     偏离本仓通行的 RESTRICT 约定，这条测试就是那个决定的守卫）；
//  3. 改参赛轮次与弃赛的边界：弃赛队改轮次必须被拒；
//  4. 判据本身：Session='1' 时两轮赛项下该队实际只打 1 场。
// ============================================================================

func mustSeat(t *testing.T, svc *service.Service, name string, order int) *model.Seat {
	t.Helper()
	seat, err := svc.CreateSeat(operatorCtx("运营A"), name, order)
	if err != nil {
		t.Fatalf("建赛台 %s 失败: %v", name, err)
	}
	return seat
}

// auditsOf 按动作读审计（时间倒序）。
func auditsOf(t *testing.T, svc *service.Service, action model.AuditAction) []model.AuditLog {
	t.Helper()
	logs, err := svc.AuditLogs(operatorCtx("运营A"), store.AuditFilter{
		Action: string(action), Limit: 100,
	})
	if err != nil {
		t.Fatalf("查审计（%s）失败: %v", action, err)
	}
	return logs
}

// reload 读回队伍 —— 断言只认库里的值。
func reload(t *testing.T, svc *service.Service, id int64) *model.Team {
	t.Helper()
	got, err := svc.GetTeam(context.Background(), id)
	if err != nil {
		t.Fatalf("读回队伍 %d 失败: %v", id, err)
	}
	return got
}

// cfgAuditsOf 某支队伍的「改配置」审计（时间倒序）。
//
// 必须按 target 过滤：建赛项、建队伍本身也写「改配置」，
// 不过滤就会把「建队伍」也算进「改参赛轮次」的条数里（本文件第一版就踩了这个）。
func cfgAuditsOf(t *testing.T, svc *service.Service, team *model.Team) []model.AuditLog {
	t.Helper()
	want := team.Name + "（" + team.TeamNo + "）"
	var out []model.AuditLog
	for _, l := range auditsOf(t, svc, model.ActConfig) {
		if l.Target == want {
			out = append(out, l)
		}
	}
	return out
}

// ---------------------------------------------------------------------------

func TestTeamSeatAssignClearAndAudit(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")

	// 新建队伍是「未排台」+「两轮」—— 未排台是正常中间态，不是错误
	if team.SeatID != nil || team.SeatOrder != 0 {
		t.Fatalf("新建队伍应未排台，实际 seatId=%v order=%d", team.SeatID, team.SeatOrder)
	}
	if team.Session != model.SessionBoth {
		t.Fatalf("新建队伍参赛轮次应为 both，实际 %q", team.Session)
	}

	seat1 := mustSeat(t, svc, "1 号台", 1)
	seat2 := mustSeat(t, svc, "2 号台", 2)

	// 首次归台
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat1.ID, 2, "首次分台"); err != nil {
		t.Fatalf("归台失败: %v", err)
	}
	got := reload(t, svc, team.ID)
	if got.SeatID == nil || *got.SeatID != seat1.ID || got.SeatOrder != 2 {
		t.Fatalf("归台未落库：seatId=%v order=%d", got.SeatID, got.SeatOrder)
	}

	// 换台（顺位一起改）
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat2.ID, 1, "该台改作他项"); err != nil {
		t.Fatalf("换台失败: %v", err)
	}
	got = reload(t, svc, team.ID)
	if got.SeatID == nil || *got.SeatID != seat2.ID || got.SeatOrder != 1 {
		t.Fatalf("换台未落库：seatId=%v order=%d", got.SeatID, got.SeatOrder)
	}

	// 审计必须能读出「从哪到哪」—— 这是排查「这队怎么跑到 2 号台去了」的唯一线索
	logs := auditsOf(t, svc, model.ActSeat)
	if len(logs) != 2 {
		t.Fatalf("应有 2 条「调赛台」审计，实际 %d 条", len(logs))
	}
	if logs[0].Before != "赛台 #1 · 顺位 2" || logs[0].After != "赛台 #2 · 顺位 1" {
		t.Fatalf("换台审计内容不对：%q → %q", logs[0].Before, logs[0].After)
	}
	if logs[1].Before != "未排台" || logs[1].After != "赛台 #1 · 顺位 2" {
		t.Fatalf("首次归台审计应从未排台起算：%q → %q", logs[1].Before, logs[1].After)
	}

	// 幂等：同值再点一次，不写库也不留痕（现场「点一下没反应就再点一下」很常见）
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat2.ID, 1, "重复点击"); err != nil {
		t.Fatalf("重复归台不应报错: %v", err)
	}
	if n := len(auditsOf(t, svc, model.ActSeat)); n != 2 {
		t.Fatalf("重复点击不应新增审计，实际 %d 条", n)
	}

	// 取消归台：seatId=0 → 未排台，且顺位一并归零
	zero := int64(0)
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &zero, 0, "该台撤销"); err != nil {
		t.Fatalf("取消归台失败: %v", err)
	}
	got = reload(t, svc, team.ID)
	if got.SeatID != nil {
		t.Fatalf("取消归台后 seatId 应为 NULL，实际 %v", *got.SeatID)
	}
	if got.SeatOrder != 0 {
		t.Fatalf("取消归台后顺位应归零，实际 %d", got.SeatOrder)
	}
	logs = auditsOf(t, svc, model.ActSeat)
	if logs[0].After != "未排台" || logs[0].Before != "赛台 #2 · 顺位 1" {
		t.Fatalf("取消归台审计内容不对：%q → %q", logs[0].Before, logs[0].After)
	}
}

func TestTeamSeatValidation(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")
	seat := mustSeat(t, svc, "1 号台", 1)

	// 赛台不存在 → ErrNotFound（服务层先查赛台，而不是把外键错误冒到界面）
	missing := int64(999999)
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &missing, 1, "分台"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("归到不存在的赛台应返回 ErrNotFound，实际 %v", err)
	}

	// 顺位从 1 起
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat.ID, 0, "分台"); err == nil {
		t.Fatal("顺位 0 应被拒绝")
	}
	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat.ID, -1, "分台"); err == nil {
		t.Fatal("负顺位应被拒绝")
	}

	// 队伍不存在
	if _, err := svc.AssignTeamSeat(ctx, 999999, &seat.ID, 1, "分台"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的队伍应返回 ErrNotFound，实际 %v", err)
	}

	// 校验失败不该留下半截数据
	if got := reload(t, svc, team.ID); got.SeatID != nil {
		t.Fatalf("校验失败不应写入归台，实际 seatId=%v", *got.SeatID)
	}
}

// TestDeleteSeatReturnsTeamsToUnassigned 守住 0016 的 ON DELETE SET NULL 决定。
//
// 本仓通行的约定是「所有 *_id 外键一律 RESTRICT」，归台刻意不遵守：
// 赛台是赛事级资源，删台是常规编排动作（改场地 / 减台位）。若用 RESTRICT，
// 删台会被「该台下还有队伍」拦下，运营只能先逐队取消归台 ——
// 而队伍并没有丢，它只是回到「未排台」。这条测试改成 RESTRICT 会立刻红。
func TestDeleteSeatReturnsTeamsToUnassigned(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")
	seat := mustSeat(t, svc, "1 号台", 1)

	if _, err := svc.AssignTeamSeat(ctx, team.ID, &seat.ID, 3, "首次分台"); err != nil {
		t.Fatalf("归台失败: %v", err)
	}

	if err := svc.DeleteSeat(ctx, seat.ID, "场地调整，撤销该台"); err != nil {
		t.Fatalf("删赛台失败（0016 之后应可删）: %v", err)
	}

	got := reload(t, svc, team.ID)
	if got.SeatID != nil {
		t.Fatalf("删台后队伍应回到未排台，实际 seatId=%v", *got.SeatID)
	}
	if got.SeatOrder != 0 {
		t.Fatalf("删台后顺位应归零，实际 %d", got.SeatOrder)
	}
}

func TestTeamSessionSetAndBoundaries(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")
	ev := mustEvent(t, svc, brainPlanetEvent())
	team := mustTeam(t, svc, ev.ID, "1001", "星河队", "小学组")

	// 缺原因 → 拒绝：它会改变「发布门是否就位」与取优轮口径
	if _, err := svc.SetTeamSession(ctx, team.ID, model.SessionRound1, ""); !errors.Is(err, service.ErrReasonRequired) {
		t.Fatalf("改参赛轮次缺原因应返回 ErrReasonRequired，实际 %v", err)
	}

	// 下午缺席 → 只打第 1 轮
	if _, err := svc.SetTeamSession(ctx, team.ID, model.SessionRound1, "该队下午缺席，已与领队确认"); err != nil {
		t.Fatalf("设置参赛轮次失败: %v", err)
	}
	got := reload(t, svc, team.ID)
	if got.Session != model.SessionRound1 {
		t.Fatalf("参赛轮次未落库，实际 %q", got.Session)
	}
	// 判据：两轮赛项下该队实际只打 1 场（交集），这正是「按队伍参赛轮次判」的落点
	if rounds := got.Session.Rounds([]int{1, 2}); fmt.Sprint(rounds) != "[1]" {
		t.Fatalf("只打第 1 轮的队在两轮赛项下应只打 [1]，实际 %v", rounds)
	}

	// 审计带旧值 → 新值。旧值必须是库里的真实口径（both → 「两轮」），
	// 不能写成「两轮（默认）」：库里存的就是 both，说成「默认」会让人
	// 以为这队从来没设过参赛轮次。
	logs := cfgAuditsOf(t, svc, team)
	if len(logs) != 1 {
		t.Fatalf("应有 1 条该队伍的「改配置」审计，实际 %d 条", len(logs))
	}
	if logs[0].Before != "参赛轮次 两轮" || logs[0].After != "参赛轮次 仅第 1 轮" {
		t.Fatalf("参赛轮次审计内容不对：%q → %q", logs[0].Before, logs[0].After)
	}

	// 幂等
	if _, err := svc.SetTeamSession(ctx, team.ID, model.SessionRound1, "重复提交"); err != nil {
		t.Fatalf("同值重复设置不应报错: %v", err)
	}
	if n := len(cfgAuditsOf(t, svc, team)); n != 1 {
		t.Fatalf("同值重复设置不应新增审计，实际 %d 条", n)
	}

	// 非法取值
	if _, err := svc.SetTeamSession(ctx, team.ID, model.TeamSession("全打"), "试一试"); err == nil {
		t.Fatal("非法参赛轮次应被拒绝")
	}

	// 弃赛队改轮次 → 拒绝并指向「恢复」：
	// 该字段对弃赛队不产生任何输出，而改它极易被误当成「恢复参赛」
	if _, err := svc.WithdrawTeam(ctx, team.ID, "队伍解散"); err != nil {
		t.Fatalf("弃赛失败: %v", err)
	}
	if _, err := svc.SetTeamSession(ctx, team.ID, model.SessionBoth, "改回两轮"); err == nil {
		t.Fatal("弃赛队改参赛轮次应被拒绝")
	}
}
