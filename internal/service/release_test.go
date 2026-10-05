package service_test

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 发布单元与移交 / 发布状态机（0006）集成测试 —— 打到真实 PG
// ============================================================================

// newReleaseFixture 建一个赛项，返回赛项 ID。
func newReleaseFixture(t *testing.T, svc *service.Service) string {
	t.Helper()
	ev, err := svc.CreateEvent(operatorCtx("运营A"), brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	return ev.ID
}

// TestReleaseLifecycle 四态主链路：未移交 → 移交 → 接收 → 发布，每步都留痕。
func TestReleaseLifecycle(t *testing.T) {
	svc, _ := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	ctx := operatorCtx("裁判长C")

	u, err := svc.EnsureReleaseUnit(ctx, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("取发布单元失败: %v", err)
	}
	if u.Status != model.ReleaseNotHanded {
		t.Fatalf("新建单元应为未移交，实际 %q", u.Status)
	}
	if u.HandoverLabel != "未移交" || u.PublishLabel != "— 未移交" {
		t.Fatalf("未移交的两列文案不对：%q / %q", u.HandoverLabel, u.PublishLabel)
	}

	// ① 移交（裁判长）
	u, err = svc.HandOverRelease(ctx, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("移交失败: %v", err)
	}
	if u.Status != model.ReleaseHanded {
		t.Fatalf("移交后应为 handed，实际 %q", u.Status)
	}
	if u.HandedBy != "裁判长C" || u.HandedAt == nil {
		t.Fatalf("移交人与时间应落库：%q / %v", u.HandedBy, u.HandedAt)
	}

	// ② 接收（工作人员）
	u, err = svc.ReceiveRelease(operatorCtx("工作人员A"), u.ID)
	if err != nil {
		t.Fatalf("接收失败: %v", err)
	}
	if u.Status != model.ReleasePending {
		t.Fatalf("接收后应为 pending，实际 %q", u.Status)
	}
	if u.PublishLabel != "⏳ 待发布" {
		t.Fatalf("待发布文案应为「⏳ 待发布」，实际 %q", u.PublishLabel)
	}

	// ③ 发布（运营）
	u, err = svc.PublishRelease(operatorCtx("运营B"), u.ID)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	if u.Status != model.ReleasePublished {
		t.Fatalf("发布后应为 published，实际 %q", u.Status)
	}
	// 发布人 / 时间即 P13 表格最后一列
	if u.PublishedBy != "运营B" || u.PublishedAt == nil {
		t.Fatalf("发布人与时间应落库：%q / %v", u.PublishedBy, u.PublishedAt)
	}
	if u.PublishLabel != "✓ 运营已发布" {
		t.Fatalf("已发布文案应为「✓ 运营已发布」，实际 %q", u.PublishLabel)
	}

	// 每步都留痕
	for _, act := range []model.AuditAction{
		model.ActHandOver, model.ActReceive, model.ActPublish,
	} {
		if n := countAction(t, svc, act); n != 1 {
			t.Fatalf("%s 应留 1 条审计，实际 %d 条", act, n)
		}
	}
}

// TestReleasePublishRequiresHandover 未移交不得发布。
func TestReleasePublishRequiresHandover(t *testing.T) {
	svc, _ := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	ctx := operatorCtx("运营B")

	u, err := svc.EnsureReleaseUnit(ctx, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("取发布单元失败: %v", err)
	}
	if _, err := svc.PublishRelease(ctx, u.ID); err != service.ErrReleaseNotPublishable {
		t.Fatalf("未移交就发布应返回 ErrReleaseNotPublishable，实际 %v", err)
	}

	// 移交后即可发布（不必等接收）
	if _, err := svc.HandOverRelease(operatorCtx("裁判长C"), eventID, "小学组", nil); err != nil {
		t.Fatalf("移交失败: %v", err)
	}
	if _, err := svc.PublishRelease(ctx, u.ID); err != nil {
		t.Fatalf("已移交后应可发布，实际失败: %v", err)
	}
}

// TestReleaseRepublish 已发布后标记重发：回退到待发布，但保留上一次发布人。
func TestReleaseRepublish(t *testing.T) {
	svc, _ := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	ctx := operatorCtx("裁判长C")

	u, err := svc.HandOverRelease(ctx, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("移交失败: %v", err)
	}
	u, err = svc.PublishRelease(operatorCtx("运营B"), u.ID)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	before := u.PublishedBy

	u, err = svc.MarkRepublish(operatorCtx("系统"), u.ID, "改分申请已生效，需重发")
	if err != nil {
		t.Fatalf("标记重发失败: %v", err)
	}
	if u.Status != model.ReleasePending {
		t.Fatalf("重发后应回退为 pending，实际 %q", u.Status)
	}
	if !u.RepublishRequired {
		t.Fatal("重发标记应为 true")
	}
	if u.PublishLabel != "⏳ 待发布（待重发）" {
		t.Fatalf("重发文案应带「（待重发）」，实际 %q", u.PublishLabel)
	}
	// 上一次发布人保留：重发期间也要能看到上一版是谁发的
	if u.PublishedBy != before {
		t.Fatalf("重发不应清掉上次发布人：期望 %q，实际 %q", before, u.PublishedBy)
	}

	// 重发后再发布，标记应清空
	u, err = svc.PublishRelease(operatorCtx("运营B"), u.ID)
	if err != nil {
		t.Fatalf("重发后再次发布失败: %v", err)
	}
	if u.RepublishRequired {
		t.Fatal("再次发布后重发标记应清空")
	}
}

// TestReleaseEnsureIdempotent 同一（赛项, 组别, 赛台）Ensure 两次是同一个单元。
func TestReleaseEnsureIdempotent(t *testing.T) {
	svc, _ := newSvc(t)
	eventID := newReleaseFixture(t, svc)
	ctx := operatorCtx("运营A")

	a, err := svc.EnsureReleaseUnit(ctx, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("首次 Ensure 失败: %v", err)
	}
	b, err := svc.EnsureReleaseUnit(ctx, eventID, "小学组", nil)
	if err != nil {
		t.Fatalf("第二次 Ensure 失败: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("Ensure 应幂等（同一单元），实际 %d != %d", a.ID, b.ID)
	}

	// 换组别是另一个发布单元
	c, err := svc.EnsureReleaseUnit(ctx, eventID, "初中组", nil)
	if err != nil {
		t.Fatalf("换组别 Ensure 失败: %v", err)
	}
	if c.ID == a.ID {
		t.Fatal("不同组别应是不同发布单元")
	}
}
