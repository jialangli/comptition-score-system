package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 赛项配置用例
//
// 两条硬规则（对应需求确认单「锁成绩 + 锁配置」与「改配置要留痕」）：
//
//  1. 保存前必须过 engine.ValidateEvent；有 error 直接拒绝，warning 随响应带回。
//  2. 改动已存在的赛项前，先把**旧配置全量快照**写入 config_snapshots，
//     再把字段级差异写进 audit_logs。事后能回答「谁在什么时候把什么改成了什么」。
// ============================================================================

// configPayload 配置快照的载荷结构。
//
// 带上 schemaVersion 是为了将来字段演进时能识别快照是新版还是旧版 ——
// 没有版本号的快照，半年后没人敢用它回滚。
type configPayload struct {
	SchemaVersion string        `json:"schemaVersion"`
	Events        []model.Event `json:"events"`
}

// SchemaVersion 当前配置结构版本。
const SchemaVersion = "v1.0"

// ValidateConfig 只做校验不落库，供「保存前预检」与 `/events/{id}/validate` 使用。
func (s *Service) ValidateConfig(ev *model.Event) engine.ValidationResult {
	return engine.ValidateEvent(ev)
}

// GetEvent 读取赛项。
func (s *Service) GetEvent(ctx context.Context, id string) (*model.Event, error) {
	return s.ro().Events.Get(ctx, id)
}

// ListEvents 列出全部赛项。
func (s *Service) ListEvents(ctx context.Context) ([]model.Event, error) {
	return s.ro().Events.List(ctx)
}

// CreateEvent 新建赛项。
//
// 事务内三件事：写赛项 → 写任务项 → 留痕。任一步失败整体回滚，
// 不会出现「赛项建好了但没有任务项」这种半成品。
func (s *Service) CreateEvent(ctx context.Context, ev *model.Event) (*model.Event, error) {
	if res := engine.ValidateEvent(ev); !res.OK() {
		return nil, &ValidationError{Result: res}
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Events.Create(ctx, ev); err != nil {
			return err
		}
		if err := r.Events.ReplaceTasks(ctx, ev.ID, ev.Tasks); err != nil {
			return err
		}
		// 新建时留一份基线快照：后面出争议可以回答「初版规则是什么」
		if _, err := r.CfgSnaps.Create(ctx, "新建赛项基线："+ev.Name,
			configPayload{SchemaVersion: SchemaVersion, Events: []model.Event{*ev}}); err != nil {
			return err
		}
		return log(ctx, r, model.ActConfig, fmt.Sprintf("赛项 %s（%s）", ev.Name, ev.ID),
			"—", describeEvent(ev), "新建赛项")
	}); err != nil {
		return nil, err
	}
	return s.ro().Events.Get(ctx, ev.ID)
}

// UpdateEvent 更新赛项配置。
//
// 流程：读旧配置 → 校验新配置 → 快照旧配置 → 写新配置 → 留痕字段级差异。
// 先读旧值再开事务，是因为校验失败时不应该占用事务；
// 而快照与写入必须在同一事务内，否则会出现「改了但没快照」。
func (s *Service) UpdateEvent(ctx context.Context, ev *model.Event, reason string) (*model.Event, error) {
	old, err := s.ro().Events.Get(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	if res := engine.ValidateEvent(ev); !res.OK() {
		return nil, &ValidationError{Result: res}
	}
	if strings.TrimSpace(reason) == "" {
		reason = "赛项配置调整"
	}

	diff := diffEvent(old, ev)
	if err := s.tx(ctx, func(r store.Repos) error {
		// 旧配置全量留档（可回滚）
		if _, err := r.CfgSnaps.Create(ctx, "改配置前留档："+old.Name,
			configPayload{SchemaVersion: SchemaVersion, Events: []model.Event{*old}}); err != nil {
			return err
		}
		if err := r.Events.Update(ctx, ev); err != nil {
			return err
		}
		// 任务项整体替换：运营在界面上是「一次提交全部任务」，替换能保证最终状态与提交一致
		if err := r.Events.ReplaceTasks(ctx, ev.ID, ev.Tasks); err != nil {
			return err
		}
		return log(ctx, r, model.ActConfig, fmt.Sprintf("赛项 %s（%s）", ev.Name, ev.ID),
			diff.Before, diff.After, reason)
	}); err != nil {
		return nil, err
	}
	return s.ro().Events.Get(ctx, ev.ID)
}

// DeleteEvent 删除赛项。
//
// 已被队伍或场次引用时返回 store.ErrInUse（数据库外键 RESTRICT 兜底），
// 提示先清理引用 —— 带成绩的赛项不允许一键消失。
func (s *Service) DeleteEvent(ctx context.Context, id string) error {
	ev, err := s.ro().Events.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(r store.Repos) error {
		if err := r.Events.Delete(ctx, id); err != nil {
			return err
		}
		return log(ctx, r, model.ActConfig, fmt.Sprintf("赛项 %s（%s）", ev.Name, id),
			describeEvent(ev), "已删除", "删除赛项")
	})
}

// ---------------------------------------------------------------------------
// 配置快照与回滚
// ---------------------------------------------------------------------------

// CreateConfigSnapshot 主动为「当前全部赛项配置」留一份快照。
func (s *Service) CreateConfigSnapshot(ctx context.Context, note string) (int64, error) {
	events, err := s.ListEvents(ctx)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(note) == "" {
		note = "手工快照"
	}
	return s.ro().CfgSnaps.Create(ctx, note,
		configPayload{SchemaVersion: SchemaVersion, Events: events})
}

// ListConfigSnapshots 列出快照（不含回滚操作）。
func (s *Service) ListConfigSnapshots(ctx context.Context, limit int) ([]model.ConfigSnapshot, error) {
	return s.ro().CfgSnaps.List(ctx, limit)
}

// RestoreConfigSnapshot 用快照覆盖当前配置。
//
// 两点刻意的保守设计：
//
//  1. **不删除**快照中不存在的赛项。回滚的目的是「把改错的值改回来」，
//     不是「把后来新建的赛项删掉」—— 后者会连带影响已录成绩，风险远大于收益。
//  2. 恢复前先为当前状态再留一份快照。这样「回滚」本身也是可回滚的，
//     否则运营点错一次就永久失去了刚才的配置。
func (s *Service) RestoreConfigSnapshot(ctx context.Context, snapshotID int64, reason string) error {
	snap, err := s.ro().CfgSnaps.Get(ctx, snapshotID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snap.Payload)
	if err != nil {
		return err
	}
	var payload configPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("快照内容无法解析（可能来自不兼容的旧版本）: %w", err)
	}
	if len(payload.Events) == 0 {
		return fmt.Errorf("快照 #%d 中没有赛项配置，拒绝执行回滚", snapshotID)
	}
	// 逐项校验：宁可不回滚，也不能把一份不合法的配置写进库里
	for i := range payload.Events {
		if res := engine.ValidateEvent(&payload.Events[i]); !res.OK() {
			return &ValidationError{Result: res}
		}
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("回滚到快照 #%d", snapshotID)
	}

	current, err := s.ListEvents(ctx)
	if err != nil {
		return err
	}

	return s.tx(ctx, func(r store.Repos) error {
		// 回滚前留档当前状态 —— 让回滚可逆
		if _, err := r.CfgSnaps.Create(ctx, "回滚前自动留档",
			configPayload{SchemaVersion: SchemaVersion, Events: current}); err != nil {
			return err
		}

		existing := make(map[string]model.Event, len(current))
		for _, e := range current {
			existing[e.ID] = e
		}

		var touched []string
		for i := range payload.Events {
			ev := &payload.Events[i]
			if _, ok := existing[ev.ID]; ok {
				if err := r.Events.Update(ctx, ev); err != nil {
					return err
				}
			} else {
				if err := r.Events.Create(ctx, ev); err != nil {
					return err
				}
			}
			if err := r.Events.ReplaceTasks(ctx, ev.ID, ev.Tasks); err != nil {
				return err
			}
			touched = append(touched, ev.Name+"（"+ev.ID+"）")
		}
		sort.Strings(touched)

		return log(ctx, r, model.ActConfig,
			fmt.Sprintf("配置回滚（快照 #%d）", snapshotID),
			describeEvents(current), "恢复为快照内容："+strings.Join(touched, "、"), reason)
	})
}

// ---------------------------------------------------------------------------
// 描述与差异
// ---------------------------------------------------------------------------

// eventDiff 一次配置改动的字段级差异。
type eventDiff struct {
	Before string
	After  string
}

// diffEvent 生成「旧 → 新」的可读差异，只列出真正变化的项。
//
// 刻意不写成完整配置的 JSON 对比：审计是给人看的（争议追溯、事后复盘），
// 一整坨 JSON 等于没写。这里只回答「哪几项被动了」。
func diffEvent(old, new *model.Event) eventDiff {
	var before, after []string
	add := func(label, b, a string) {
		if b == a {
			return
		}
		before = append(before, label+"："+b)
		after = append(after, label+"："+a)
	}

	add("名称", old.Name, new.Name)
	add("组别", strings.Join(old.Groups, "/"), strings.Join(new.Groups, "/"))
	add("计分模板", old.ScoreRule.Template.Display(), new.ScoreRule.Template.Display())
	add("奖励规则", describeBonuses(old), describeBonuses(new))
	add("扣分规则", old.PenaltyRule.Template.Display(), new.PenaltyRule.Template.Display())
	add("同分裁决", describeTieBreak(old), describeTieBreak(new))
	add("奖项占比", describeTiers(old), describeTiers(new))
	add("任务项", describeTasks(old), describeTasks(new))
	add("自定义公式", describeFormula(old), describeFormula(new))

	if len(before) == 0 {
		return eventDiff{Before: "（无实质变化）", After: "（无实质变化）"}
	}
	return eventDiff{
		Before: strings.Join(before, "；"),
		After:  strings.Join(after, "；"),
	}
}

func describeEvent(ev *model.Event) string {
	if ev == nil {
		return ""
	}
	return fmt.Sprintf("名称=%s；组别=%s；计分模板=%s；任务项=%s",
		ev.Name, strings.Join(ev.Groups, "/"),
		ev.ScoreRule.Template.Display(), describeTasks(ev))
}

func describeEvents(events []model.Event) string {
	names := make([]string, 0, len(events))
	for i := range events {
		names = append(names, events[i].Name)
	}
	if len(names) == 0 {
		return "（空）"
	}
	return fmt.Sprintf("共 %d 个赛项：%s", len(names), strings.Join(names, "、"))
}

func describeTasks(ev *model.Event) string {
	parts := make([]string, 0, len(ev.Tasks))
	for i := range ev.Tasks {
		t := &ev.Tasks[i]
		parts = append(parts, fmt.Sprintf("%s(%s)", t.Name, t.Type.Display()))
	}
	if len(parts) == 0 {
		return "0 项"
	}
	return fmt.Sprintf("%d 项：%s", len(parts), strings.Join(parts, "、"))
}

func describeBonuses(ev *model.Event) string {
	parts := make([]string, 0, len(ev.BonusRules))
	for i := range ev.BonusRules {
		parts = append(parts, ev.BonusRules[i].Template.Display())
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, "+")
}

func describeTieBreak(ev *model.Event) string {
	if len(ev.RankRule.TieBreak) == 0 {
		return "未配置"
	}
	parts := make([]string, 0, len(ev.RankRule.TieBreak))
	for _, tb := range ev.RankRule.TieBreak {
		switch tb {
		case "score":
			parts = append(parts, "得分高者优先")
		case "time":
			parts = append(parts, "用时少者优先")
		default:
			parts = append(parts, tb)
		}
	}
	return strings.Join(parts, " → ")
}

func describeTiers(ev *model.Event) string {
	names := make([]string, 0, len(ev.RankRule.AwardTiers))
	for k := range ev.RankRule.AwardTiers {
		names = append(names, k)
	}
	if len(names) == 0 {
		return "未配置"
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", n, ev.RankRule.AwardTiers[n]*100))
	}
	return strings.Join(parts, "、")
}

func describeFormula(ev *model.Event) string {
	if ev.CustomFormula == nil {
		return "无"
	}
	return *ev.CustomFormula
}
