package postgres

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// EventStore 赛项仓储。
type EventStore struct{ q querier }

// eventColumns 赛项列清单（集中一处，避免 SELECT 与 Scan 顺序不一致）。
const eventColumns = `id, name, groups, phases, score_rule, bonus_rules, penalty_rule,
	rank_rule, custom_formula, config_version, created_at, updated_at`

func scanEvent(row interface{ Scan(...any) error }) (*model.Event, error) {
	var ev model.Event
	if err := row.Scan(
		&ev.ID, &ev.Name, &ev.Groups, &ev.Phases, &ev.ScoreRule, &ev.BonusRules, &ev.PenaltyRule,
		&ev.RankRule, &ev.CustomFormula, &ev.ConfigVersion, &ev.CreatedAt, &ev.UpdatedAt,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	return &ev, nil
}

// Create 新增赛项。任务项需另行调用 ReplaceTasks。
func (s *EventStore) Create(ctx context.Context, ev *model.Event) error {
	if ev.ConfigVersion == "" {
		ev.ConfigVersion = "v1.0"
	}
	_, err := s.q.Exec(ctx, `
		INSERT INTO events (id, name, groups, phases, score_rule, bonus_rules, penalty_rule,
		                    rank_rule, custom_formula, config_version, contest_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		ev.ID, ev.Name, jsonArg(ev.Groups), jsonArg(ev.Phases), ev.ScoreRule, jsonArg(ev.BonusRules),
		ev.PenaltyRule, ev.RankRule, ev.CustomFormula, ev.ConfigVersion,
		store.CurrentContest(ctx))
	return mapError(err)
}

// Get 读取赛项（含任务项）。
func (s *EventStore) Get(ctx context.Context, id string) (*model.Event, error) {
	ev, err := scanEvent(s.q.QueryRow(ctx,
		`SELECT `+eventColumns+` FROM events WHERE id = $1 AND contest_id = $2`,
		id, store.CurrentContest(ctx)))
	if err != nil {
		return nil, err
	}
	if err := s.loadTasks(ctx, ev); err != nil {
		return nil, err
	}
	return ev, nil
}

// List 返回全部赛项（含任务项），按 id 升序保证界面顺序稳定。
func (s *EventStore) List(ctx context.Context) ([]model.Event, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+eventColumns+` FROM events WHERE contest_id = $1 ORDER BY id`,
		store.CurrentContest(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	// 先把所有赛项读出来，再一次性拉取全部任务项并按 event_id 归位 ——
	// 避免每个赛项一次查询（N+1）。
	var out []model.Event
	idx := map[string]int{}
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		idx[ev.ID] = len(out)
		out = append(out, *ev)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	if len(out) == 0 {
		return out, nil
	}

	trows, err := s.q.Query(ctx, `
		SELECT event_id, id, name, type, max_score::float8, weight::float8,
		       unit, control, enum_map, sort_order
		FROM tasks ORDER BY event_id, sort_order, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer trows.Close()

	for trows.Next() {
		var eventID string
		t, err := scanTask(trows, &eventID)
		if err != nil {
			return nil, err
		}
		if i, ok := idx[eventID]; ok {
			out[i].Tasks = append(out[i].Tasks, *t)
		}
	}
	return out, mapError(trows.Err())
}

// Update 更新赛项的规则部分。
func (s *EventStore) Update(ctx context.Context, ev *model.Event) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE events SET name=$2, groups=$3, phases=$4, score_rule=$5, bonus_rules=$6,
		                  penalty_rule=$7, rank_rule=$8, custom_formula=$9, config_version=$10
		WHERE id = $1 AND contest_id = $11`,
		ev.ID, ev.Name, jsonArg(ev.Groups), jsonArg(ev.Phases), ev.ScoreRule, jsonArg(ev.BonusRules),
		ev.PenaltyRule, ev.RankRule, ev.CustomFormula, ev.ConfigVersion,
		store.CurrentContest(ctx))
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ReplaceTasks 整体替换赛项的任务项。
//
// 采用「先删后插」而不是逐条 upsert：任务项的增删改在配置页是一次提交的，
// 整体替换能让最终状态与提交内容严格一致，不会残留已删除的任务。
// 调用方需保证在事务内执行。
func (s *EventStore) ReplaceTasks(ctx context.Context, eventID string, tasks []model.Task) error {
	if _, err := s.q.Exec(ctx,
		`DELETE FROM tasks WHERE event_id = $1 AND contest_id = $2`,
		eventID, store.CurrentContest(ctx)); err != nil {
		return mapError(err)
	}
	for i := range tasks {
		t := &tasks[i]
		order := t.SortOrder
		if order == 0 {
			order = i // 未显式指定顺序时按数组下标，保持运营在界面上的排列
		}
		var enumArg any
		if len(t.EnumMap) > 0 {
			enumArg = t.EnumMap
		}
		var maxScore any
		if t.MaxScore != nil {
			maxScore = *t.MaxScore
		}
		if _, err := s.q.Exec(ctx, `
			INSERT INTO tasks (event_id, id, name, type, max_score, weight, unit, control, enum_map, sort_order, contest_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			eventID, t.ID, t.Name, string(t.Type), maxScore, t.Weight,
			// 空量词写成 NULL 而不是空串：空态只留一种（与 control / enum_map 同口径），
			// 否则读回来分不清「没配」和「配了个空串」。
			nonEmpty(t.Unit), nonEmpty(string(t.Control)), enumArg, order, store.CurrentContest(ctx),
		); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// Delete 删除赛项。若已被队伍或场次引用，数据库外键（RESTRICT）会拦下并返回 ErrInUse。
func (s *EventStore) Delete(ctx context.Context, id string) error {
	tag, err := s.q.Exec(ctx,
		`DELETE FROM events WHERE id = $1 AND contest_id = $2`, id, store.CurrentContest(ctx))
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// loadTasks 读取单个赛项的任务项。
func (s *EventStore) loadTasks(ctx context.Context, ev *model.Event) error {
	rows, err := s.q.Query(ctx, `
		SELECT id, name, type, max_score::float8, weight::float8, unit, control, enum_map, sort_order
		FROM tasks WHERE event_id = $1 AND contest_id = $2 ORDER BY sort_order, id`,
		ev.ID, store.CurrentContest(ctx))
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()

	for rows.Next() {
		t, err := scanTask(rows, nil)
		if err != nil {
			return err
		}
		ev.Tasks = append(ev.Tasks, *t)
	}
	return mapError(rows.Err())
}

// scanTask 读取一行任务项；eventID 非 nil 时一并扫描首列 event_id。
//
// max_score / weight 用 NUMERIC 存储，这里显式 ::float8 转换 ——
// 让 Go 侧拿到确定的 float64，不必依赖驱动对 numeric 的隐式映射。
func scanTask(rows interface{ Scan(...any) error }, eventID *string) (*model.Task, error) {
	var t model.Task
	var typeStr string
	var maxScore *float64
	var unit *string
	var control *string
	var enumRaw []byte

	dest := []any{&t.ID, &t.Name, &typeStr, &maxScore, &t.Weight, &unit, &control, &enumRaw, &t.SortOrder}
	if eventID != nil {
		dest = append([]any{eventID}, dest...)
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, notFoundIfNoRows(err)
	}

	t.Type = model.TaskType(typeStr)
	t.MaxScore = maxScore
	if unit != nil {
		t.Unit = *unit
	}
	if control != nil {
		t.Control = model.Control(*control)
	}
	if err := scanJSONB(enumRaw, &t.EnumMap); err != nil {
		return nil, err
	}
	return &t, nil
}
