package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 聚合告警（图集 12 缺口补齐）—— 只读派生，不新增任何存储
//
// demo 的「告警收件箱」是动作发生时逐条 push 进 S.alerts 的；后端没有告警表，
// 这里改为**从既有业务表现算**：待审批改分、待裁定争议、超时催办、自批、
// 取消资格。好处是没有「发告警」这个写动作，就不存在「漏发 / 重复发」——
// 告警永远与台账一致（同一份数据的两种投影）。
//
// 口径与 demo 对齐：
//   - 超时阈值 30 分钟（CASE_TIMEOUT_MIN，与平板端 P8 note 8 同源）
//   - 自批 = 改分申请的申请人与审批人同一账号（可自我批准但必须留痕可查）
//   - 取消资格 = 已裁定且结论为 disqualify 的争议单（改判后自动不再命中）
//
// 由 AlertsAt(now) 驱动时间判断：集成测试用注入时钟验证超时，避免睡等真实时间。
// ============================================================================

// CaseTimeoutMin 裁定 / 审批超时阈值（分钟）。与 demo 看板、平板端 P8 note 8 同口径。
const CaseTimeoutMin = 30

// Alert 一条聚合告警。
type Alert struct {
	Type   string    `json:"type"`   // 改分待审批 / 争议待裁定 / 超时催办 / 自批告警 / 取消资格
	Level  string    `json:"level"`  // warn=待办 / err=超时 / danger=最高危档
	Target string    `json:"target"` // 队伍（轮次）等可读对象
	Detail string    `json:"detail"`
	Ref    string    `json:"ref"` // 单号引用，如 D-012 / 改分单 #7
	Ts     time.Time `json:"ts"`
	AgeMin int       `json:"ageMin"` // 距今分钟数（超时判断依据）
}

// AlertsReport 聚合结果。Summary 按 Type 计数，供看板 KPI 直接使用。
type AlertsReport struct {
	TimeoutMin int            `json:"timeoutMin"`
	Summary    map[string]int `json:"summary"`
	Alerts     []Alert        `json:"alerts"`
}

// Alerts 当前时刻的聚合告警。
func (s *Service) Alerts(ctx context.Context) (*AlertsReport, error) {
	return s.AlertsAt(ctx, time.Now())
}

// AlertsAt 指定时刻的聚合告警（测试注入时钟用）。
func (s *Service) AlertsAt(ctx context.Context, now time.Time) (*AlertsReport, error) {
	changes, err := s.ro().Changes.ListByContest(ctx)
	if err != nil {
		return nil, err
	}
	openDisputes, err := s.ro().Disputes.ListOpen(ctx)
	if err != nil {
		return nil, err
	}
	dqs, err := s.ro().Disputes.ListDisqualifications(ctx)
	if err != nil {
		return nil, err
	}

	// 队伍名缓存：几类告警的 target 都要人话，同一队可能出现在多条里
	teams := map[int64]model.Team{}
	labelOf := func(teamID int64) string {
		if t, ok := teams[teamID]; ok {
			return teamLabel(&t)
		}
		t, err := s.ro().Teams.Get(ctx, teamID)
		if err != nil {
			return fmt.Sprintf("队伍 #%d（已删除）", teamID)
		}
		teams[teamID] = *t
		return teamLabel(t)
	}
	ageMin := func(ts time.Time) int {
		if now.Before(ts) {
			return 0
		}
		return int(now.Sub(ts).Minutes())
	}

	alerts := make([]Alert, 0, len(changes)+len(openDisputes)+len(dqs))
	summary := map[string]int{}
	add := func(a Alert) {
		summary[a.Type]++
		alerts = append(alerts, a)
	}

	// 改分申请：待审批 → 待办/超时；已审批且申请人=审批人 → 自批（最高危档）
	for i := range changes {
		c := &changes[i]
		target := fmt.Sprintf("%s 第 %d 轮", labelOf(c.TeamID), c.RoundNo)
		ref := fmt.Sprintf("改分单 #%d", c.ID)
		detail := fmt.Sprintf("%.1f → %.1f · 申请人 %s · %s",
			c.Before, c.After, c.Operator, c.Reason)
		// 单内声明了拟改用时 → 详情带上两端可读时间（2026/10/14）。
		// 告警是裁判长判断「这单要不要批」的第一屏：改动涉及并列裁决的 tiebreak
		// 时，必须在告警上就看得见，而不是点进详情才发现。
		if c.ChangesTime() {
			before := "—"
			if c.BeforeTime != nil {
				before = model.FormatDuration(*c.BeforeTime)
			}
			detail += fmt.Sprintf(" · 用时 %s → %s", before, model.FormatDuration(*c.AfterTime))
		}
		switch {
		case !c.Approved:
			age := ageMin(c.CreatedAt)
			if age >= CaseTimeoutMin {
				add(Alert{Type: "超时催办", Level: "err", Target: target,
					Detail: detail + "（待审批改分超时）", Ref: ref, Ts: c.CreatedAt, AgeMin: age})
			} else {
				add(Alert{Type: "改分待审批", Level: "warn", Target: target,
					Detail: detail, Ref: ref, Ts: c.CreatedAt, AgeMin: age})
			}
		case c.Approver != "" && c.Approver == c.Operator:
			add(Alert{Type: "自批告警", Level: "danger", Target: target,
				Detail: fmt.Sprintf("申请人 = 审批人（%s），同一账号自批 —— 请事后抽样复核", c.Approver),
				Ref:    ref, Ts: c.CreatedAt, AgeMin: ageMin(c.CreatedAt)})
		}
	}

	// 待裁定争议：待办/超时
	for i := range openDisputes {
		d := &openDisputes[i]
		d.DeriveCode()
		target := fmt.Sprintf("%s 第 %d 轮 · %s", labelOf(d.TeamID), d.RoundNo, d.Kind.Label())
		ref := "争议单 " + d.Code
		age := ageMin(d.CreatedAt)
		if age >= CaseTimeoutMin {
			add(Alert{Type: "超时催办", Level: "err", Target: target,
				Detail: d.Reason + "（待裁定超时）", Ref: ref, Ts: d.CreatedAt, AgeMin: age})
		} else {
			add(Alert{Type: "争议待裁定", Level: "warn", Target: target,
				Detail: d.Reason, Ref: ref, Ts: d.CreatedAt, AgeMin: age})
		}
	}

	// 取消资格：最高危档，登记即入收件箱（demo 口径：事后抽样复核）
	for i := range dqs {
		d := &dqs[i]
		d.DeriveCode()
		decided := now
		if d.DecidedAt != nil {
			decided = *d.DecidedAt
		}
		add(Alert{Type: "取消资格", Level: "danger",
			Target: fmt.Sprintf("%s 第 %d 轮", labelOf(d.TeamID), d.RoundNo),
			Detail: fmt.Sprintf("成绩作废、不进榜单 · 裁定人 %s · %s", d.Decider, d.DecisionReason),
			Ref:    "争议单 " + d.Code, Ts: decided, AgeMin: ageMin(decided)})
	}

	return &AlertsReport{TimeoutMin: CaseTimeoutMin, Summary: summary, Alerts: alerts}, nil
}
