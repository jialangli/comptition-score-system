package postgres

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ContestRulesStore 赛事级规则仓储（一行一赛事）。
type ContestRulesStore struct{ q querier }

// Get 读取当前赛事的规则。
//
// 与其它 Get 不同：**没有记录时返回默认值，而不是 ErrNotFound** ——
// 本表是「可选覆盖」，没有行就等于用代码里的默认口径（不递补 = 保留名次空缺）。
//
// 另一个做法是给每个赛事预插一行，但那样会把默认值复制进数据里：
// 以后改默认口径，存量赛事跟不上；而且「从未设过」与「显式设成默认」再也分不开 ——
// 而这两件事在排查"名次为什么长这样"时正是要问的。
func (s *ContestRulesStore) Get(ctx context.Context) (*model.ContestRules, error) {
	cid := store.CurrentContest(ctx)
	r := &model.ContestRules{ContestID: cid}
	err := s.q.QueryRow(ctx, `
		SELECT substitute_mode, substitute_note, updated_at
		FROM contest_rules WHERE contest_id=$1`, cid).
		Scan(&r.SubstituteMode, &r.SubstituteNote, &r.UpdatedAt)
	if err != nil {
		if !isNoRows(err) {
			return nil, mapError(err)
		}
		r.Normalize() // 没有行 → 补成默认口径，不要把 "" 当一种取值往外传
		return r, nil
	}
	r.Normalize()
	return r, nil
}

// Upsert 写入当前赛事的规则，并回填 updated_at。
func (s *ContestRulesStore) Upsert(ctx context.Context, r *model.ContestRules) error {
	r.Normalize()
	cid := store.CurrentContest(ctx)
	r.ContestID = cid
	err := s.q.QueryRow(ctx, `
		INSERT INTO contest_rules (contest_id, substitute_mode, substitute_note)
		VALUES ($1,$2,$3)
		ON CONFLICT (contest_id) DO UPDATE SET
			substitute_mode=EXCLUDED.substitute_mode,
			substitute_note=EXCLUDED.substitute_note,
			updated_at=now()
		RETURNING updated_at`,
		cid, string(r.SubstituteMode), r.SubstituteNote).Scan(&r.UpdatedAt)
	return mapError(err)
}
