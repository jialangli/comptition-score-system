package postgres

import (
	"context"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// EvidenceStore 留底证据仓储。
//
// 只存元数据，不存二进制本体 —— 图片本体属于对象存储的职责，
// 塞进 Postgres 会让库体积随赛事线性膨胀、备份变慢。
type EvidenceStore struct{ q querier }

const evidenceColumns = `id, team_id, round_no, kind, source, file_name, status,
	operator, seat_id, dispute_id, storage_url, created_at, synced_at`

func scanEvidence(row interface{ Scan(...any) error }) (*model.Evidence, error) {
	var e model.Evidence
	var round *int
	var kind, source, status string
	var synced *time.Time
	if err := row.Scan(
		&e.ID, &e.TeamID, &round, &kind, &source, &e.FileName, &status,
		&e.Operator, &e.SeatID, &e.DisputeID, &e.StorageURL, &e.CreatedAt, &synced,
	); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	e.RoundNo = round
	e.Kind = model.EvidenceKind(kind)
	e.Source = model.EvidenceSource(source)
	e.Status = model.EvidenceStatus(status)
	e.SyncedAt = synced
	e.DeriveLabels()
	return &e, nil
}

// Create 登记一条证据。重复（同队伍同轮次同类型同文件名）返回 ErrDuplicate ——
// 补传会重试，不去重会让「证据三件」变成「七件」，反而没法判断齐不齐。
func (s *EvidenceStore) Create(ctx context.Context, e *model.Evidence) error {
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO evidence
			(contest_id, team_id, round_no, kind, source, file_name, status,
			 operator, seat_id, dispute_id, storage_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, created_at`,
		store.CurrentContest(ctx), e.TeamID, e.RoundNo, e.Kind, e.Source, e.FileName,
		e.Status, e.Operator, e.SeatID, e.DisputeID, e.StorageURL,
	).Scan(&e.ID, &e.CreatedAt))
}

// MarkSynced 标记为已上云。
func (s *EvidenceStore) MarkSynced(ctx context.Context, id int64, storageURL string) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE evidence
		SET status='synced', synced_at=now(),
		    storage_url = CASE WHEN $3 = '' THEN storage_url ELSE $3 END
		WHERE id=$1 AND contest_id=$2`, id, store.CurrentContest(ctx), storageURL)
	return oneRow(tag, err)
}

// ListByTeam 某队全部证据；round=0 表示不按轮次过滤（发�� / 裁定类证据无轮次）。
func (s *EvidenceStore) ListByTeam(ctx context.Context, teamID int64, round int) ([]model.Evidence, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+evidenceColumns+` FROM evidence
		WHERE contest_id=$1 AND team_id=$2
		  AND ($3 = 0 OR round_no = $3)
		ORDER BY round_no NULLS LAST, id`,
		store.CurrentContest(ctx), teamID, round)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	out := make([]model.Evidence, 0)
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, mapError(rows.Err())
}

// ListPending 待上云队列：断网时先落本地，联网后补传。
func (s *EvidenceStore) ListPending(ctx context.Context) ([]model.Evidence, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+evidenceColumns+` FROM evidence
		WHERE contest_id=$1 AND status='local'
		ORDER BY created_at, id`, store.CurrentContest(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	out := make([]model.Evidence, 0)
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, mapError(rows.Err())
}
