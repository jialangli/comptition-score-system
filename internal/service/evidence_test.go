package service_test

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 留底证据库（0009）—— 打到真实 PG
//
// 核心不变式：
//   - 证据三件（成绩表 / 签名图 / 提交留底截图）是否齐全可判定
//   - 状态一律以「是否已上云」为准：先 local，补传后 synced
//   - 作废 / 冲突不抹除证据（本表与成绩解耦，无级联删除）
// ============================================================================

// newEvidenceFixture 建赛项 + 一支队伍，返回队伍 ID。
func newEvidenceFixture(t *testing.T, svc *service.Service, no string) int64 {
	t.Helper()
	ctx := operatorCtx("运营A")
	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	team, err := svc.CreateTeam(ctx, model.TeamDraft{
		EventID: ev.ID, TeamNo: no, Name: "留底队", GroupCode: "小学组",
	})
	if err != nil {
		t.Fatalf("建队伍失败: %v", err)
	}
	return team.ID
}

// recordEvidence 登记一条证据。
func recordEvidence(t *testing.T, svc *service.Service, teamID int64, round int,
	kind model.EvidenceKind, file string) *model.Evidence {
	t.Helper()
	r := round
	e := &model.Evidence{
		TeamID: teamID, RoundNo: &r, Kind: kind,
		Source:   model.SrcRefereeSubmit,
		FileName: file, Status: model.EvLocal, Operator: "张老师",
	}
	out, err := svc.RecordEvidence(operatorCtx("张老师"), e)
	if err != nil {
		t.Fatalf("登记证据 %s 失败: %v", kind, err)
	}
	return out
}

// TestEvidenceThreePieces 证据三件齐全性判定 + 上云状态流转。
func TestEvidenceThreePieces(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newEvidenceFixture(t, svc, "4001")
	ctx := operatorCtx("张老师")

	// 一件都没有 → 不齐全，缺三件
	ok, missing, err := svc.EvidenceComplete(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("查询完整性失败: %v", err)
	}
	if ok || len(missing) != 3 {
		t.Fatalf("无证据时应缺 3 件：ok=%v missing=%v", ok, missing)
	}

	// 登记三件
	sheet := recordEvidence(t, svc, teamID, 1, model.EvScoreSheet, "T-001_R1_sheet.pdf")
	sig := recordEvidence(t, svc, teamID, 1, model.EvSignature, "T-001_R1_1240.jpg")
	snap := recordEvidence(t, svc, teamID, 1, model.EvSubmitSnap, "T-001_R1_snap.jpg")

	for _, e := range []*model.Evidence{sheet, sig, snap} {
		if e.Status != model.EvLocal {
			t.Fatalf("新登记应为本地待同步，实际 %q", e.Status)
		}
		if e.KindLabel == "" || e.SourceLabel == "" || e.StatusLabel == "" {
			t.Fatalf("应带回中文标签：%+v", e)
		}
	}

	// 三件齐全
	ok, missing, err = svc.EvidenceComplete(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("查询完整性失败: %v", err)
	}
	if !ok || len(missing) != 0 {
		t.Fatalf("三件齐全后应 complete：ok=%v missing=%v", ok, missing)
	}

	// 待上云队列
	pending, err := svc.PendingEvidence(ctx)
	if err != nil {
		t.Fatalf("查待上云队列失败: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("应有 3 条待上云，实际 %d", len(pending))
	}

	// 补传上云
	if err := svc.MarkEvidenceSynced(ctx, sig.ID, "https://oss.example.com/T-001_R1_1240.jpg"); err != nil {
		t.Fatalf("标记上云失败: %v", err)
	}
	list, err := svc.TeamEvidence(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("查队伍证据失败: %v", err)
	}
	synced := 0
	for _, e := range list {
		if e.ID == sig.ID {
			if e.Status != model.EvSynced {
				t.Fatalf("标记后应为已上云，实际 %q", e.Status)
			}
			if e.SyncedAt == nil {
				t.Fatal("已上云应有同步时间")
			}
		}
		if e.Status == model.EvSynced {
			synced++
		}
	}
	if synced != 1 {
		t.Fatalf("应只有 1 条已上云，实际 %d", synced)
	}

	// 待上云队列应减少
	pending2, err := svc.PendingEvidence(ctx)
	if err != nil {
		t.Fatalf("查待上云队列失败: %v", err)
	}
	if len(pending2) != 2 {
		t.Fatalf("标记 1 条上云后应剩 2 条，实际 %d", len(pending2))
	}
}

// TestEvidenceDuplicateBlocked 补传重试不产生重复证据（否则三件会变七件）。
func TestEvidenceDuplicateBlocked(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newEvidenceFixture(t, svc, "4002")
	ctx := operatorCtx("张老师")

	recordEvidence(t, svc, teamID, 1, model.EvSignature, "T-002_R1_1240.jpg")
	// 同一（队伍, 轮次, 类型, 文件名）再登记一次 → 应被唯一索引挡下
	r := 1
	dup := &model.Evidence{
		TeamID: teamID, RoundNo: &r, Kind: model.EvSignature,
		Source: model.SrcRefereeSubmit, FileName: "T-002_R1_1240.jpg",
		Status: model.EvLocal, Operator: "张老师",
	}
	if _, err := svc.RecordEvidence(ctx, dup); err == nil {
		t.Fatal("重复登记同一条证据应被拒绝")
	}
}

// TestEvidenceSourceDistinct 产生端可区分（裁判提交 / 裁判长裁定 / 工作人员发布）。
func TestEvidenceSourceDistinct(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newEvidenceFixture(t, svc, "4003")
	ctx := operatorCtx("裁判长C")

	// 裁判长提交裁定产生的裁定单
	e := &model.Evidence{
		TeamID: teamID, RoundNo: nil, Kind: model.EvDecision,
		Source: model.SrcChiefDecide, FileName: "D-001_decision.pdf",
		Status: model.EvLocal, Operator: "裁判长C",
	}
	out, err := svc.RecordEvidence(ctx, e)
	if err != nil {
		t.Fatalf("登记裁定单失败: %v", err)
	}
	if out.SourceLabel != "裁判长提交裁定" {
		t.Fatalf("产生端文案不对：%q", out.SourceLabel)
	}
	if out.RoundNo != nil {
		t.Fatal("裁定类证据可无轮次")
	}

	// round=0 查询（不过滤轮次）应能查到它
	list, err := svc.TeamEvidence(ctx, teamID, 0)
	if err != nil {
		t.Fatalf("查队伍证据失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("不过滤轮次时应查到 1 条，实际 %d", len(list))
	}
}
