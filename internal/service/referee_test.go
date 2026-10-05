package service_test

import (
	"errors"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 裁判码（0007）集成测试 —— 打到真实 PG
//
// 重点：三种失败必须可区分（前端按此分流到 P1.5b / P1.5c），
// 以及「裁判码是赛事级凭证」这条不变式。
// ============================================================================

func TestRefereeIssueAndActivate(t *testing.T) {
	svc, _ := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	adminCtx := operatorCtx("运营A")

	c, err := svc.IssueRefereeCode(adminCtx, "张老师", model.RoleChief, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("建档发码失败: %v", err)
	}
	if len(c.Code) != 6 {
		t.Fatalf("裁判码应为 6 位，实际 %q（%d 位）", c.Code, len(c.Code))
	}
	if c.Status != model.RefereeUnused {
		t.Fatalf("新建应为未激活，实际 %q", c.Status)
	}
	if c.Role != model.RoleChief {
		t.Fatalf("身份应为 chief，实际 %q", c.Role)
	}
	// 执裁范围在建档时预绑，登录只做校验
	if c.EventID != eventID || c.GroupCode != "小学组" {
		t.Fatalf("预绑范围不对：%q / %q", c.EventID, c.GroupCode)
	}

	// 首登联网激活
	got, err := svc.ActivateReferee(operatorCtx("张老师"), c.Code, "张老师")
	if err != nil {
		t.Fatalf("激活失败: %v", err)
	}
	if got.Status != model.RefereeActivated {
		t.Fatalf("激活后应为 activated，实际 %q", got.Status)
	}
	if got.ActivatedAt == nil {
		t.Fatal("激活时间不应为空")
	}

	// 再次激活幂等（换平板 / 重装 App 都要能重新激活）
	again, err := svc.ActivateReferee(operatorCtx("张老师"), c.Code, "张老师")
	if err != nil {
		t.Fatalf("重复激活不应失败（换平板场景）: %v", err)
	}
	if again.Status != model.RefereeActivated {
		t.Fatalf("重复激活后仍应为 activated，实际 %q", again.Status)
	}
	// 重复激活不重复留痕
	if n := countAction(t, svc, model.ActRefereeActivate); n != 1 {
		t.Fatalf("重复激活应只留 1 条审计，实际 %d 条", n)
	}
	if n := countAction(t, svc, model.ActRefereeIssue); n != 1 {
		t.Fatalf("建档应留 1 条审计，实际 %d 条", n)
	}
}

// TestRefereeActivateFailures 三种失败必须可区分。
func TestRefereeActivateFailures(t *testing.T) {
	svc, _ := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	adminCtx := operatorCtx("运营A")

	c, err := svc.IssueRefereeCode(adminCtx, "张老师", model.RoleReferee, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("建档发码失败: %v", err)
	}

	// ① 码不存在 → 码无效（P1.5b）
	if _, err := svc.ActivateReferee(adminCtx, "ZZZZZZ", "张老师"); !errors.Is(err, service.ErrRefereeCodeInvalid) {
		t.Fatalf("不存在的码应返回 ErrRefereeCodeInvalid，实际 %v", err)
	}
	// ② 码存在但姓名不符 → 姓名不匹配（P1.5c）
	if _, err := svc.ActivateReferee(adminCtx, c.Code, "李老师"); !errors.Is(err, service.ErrRefereeNameMismatch) {
		t.Fatalf("姓名不符应返回 ErrRefereeNameMismatch，实际 %v", err)
	}
	// ③ 空码 / 空姓名同样按「码无效」处理（不能放过去）
	if _, err := svc.ActivateReferee(adminCtx, "", "张老师"); !errors.Is(err, service.ErrRefereeCodeInvalid) {
		t.Fatalf("空码应返回 ErrRefereeCodeInvalid，实际 %v", err)
	}
}

// TestRefereeCodeIsContestScoped 裁判码是**赛事级凭证**：旧码在新赛事无效。
func TestRefereeCodeIsContestScoped(t *testing.T) {
	svc, db := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	adminCtx := operatorCtx("运营A")

	c, err := svc.IssueRefereeCode(adminCtx, "张老师", model.RoleReferee, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("建档发码失败: %v", err)
	}

	// 另建一场赛事（FK 要求赛事先存在）
	other := &model.Contest{ID: "ct_other", Name: "另一场赛事"}
	if err := db.Repos().Contests.Create(adminCtx, other); err != nil {
		t.Fatalf("建另一场赛事失败: %v", err)
	}

	// 本赛事能激活
	if _, err := svc.ActivateReferee(adminCtx, c.Code, "张老师"); err != nil {
		t.Fatalf("本赛事应能激活: %v", err)
	}
	// 切到另一场赛事，同一个码应当查不到 —— 换赛事必须重新建档发码
	otherCtx := service.WithContest(adminCtx, "ct_other")
	if _, err := svc.ActivateReferee(otherCtx, c.Code, "张老师"); !errors.Is(err, service.ErrRefereeCodeInvalid) {
		t.Fatalf("跨赛事使用旧码应无效，实际 %v", err)
	}
}
