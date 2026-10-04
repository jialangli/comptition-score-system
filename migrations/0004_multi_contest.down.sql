-- ============================================================================
-- 0004 回滚：撤掉多赛事维度（contest_id）
--
-- 与 up 严格逆序：先删索引/约束，再删列，最后删 contests 表。
--
-- ⚠️ 回滚有损：所有赛事的 contest_id 被丢弃后，多场赛事的数据会在同一张表里
--    混在一起，且无法再按赛事区分。若已存在 ct_default 之外的赛事数据，
--    回滚前务必先备份。
-- ============================================================================

BEGIN;

-- 1. 索引（与 up 的第 5 节对应）
DROP INDEX IF EXISTS idx_audit_logs_contest;
DROP INDEX IF EXISTS idx_import_logs_contest;
DROP INDEX IF EXISTS idx_config_snapshots_contest;
DROP INDEX IF EXISTS idx_slot_snapshots_contest;
DROP INDEX IF EXISTS idx_slot_teams_contest;
DROP INDEX IF EXISTS idx_slots_contest;
DROP INDEX IF EXISTS idx_seats_contest;
DROP INDEX IF EXISTS idx_scr_contest;
DROP INDEX IF EXISTS idx_scores_contest_round;
DROP INDEX IF EXISTS idx_scores_contest;
DROP INDEX IF EXISTS idx_teams_contest;
DROP INDEX IF EXISTS idx_tasks_contest;
DROP INDEX IF EXISTS idx_events_contest;

-- 2. screen_config 主键还原为 (event_id)
ALTER TABLE screen_config DROP CONSTRAINT IF EXISTS screen_config_pkey;
ALTER TABLE screen_config ADD PRIMARY KEY (event_id);

-- 3. 唯一约束还原为「不带赛事维度」的原始形态
DROP INDEX IF EXISTS uq_slot_snapshots_contest_slot_no;
CREATE UNIQUE INDEX IF NOT EXISTS slot_snapshots_slot_id_team_no_key
    ON slot_snapshots (slot_id, team_no);

DROP INDEX IF EXISTS uq_slots_contest_seat_period;
CREATE UNIQUE INDEX IF NOT EXISTS slots_seat_id_period_key
    ON slots (seat_id, period);

DROP INDEX IF EXISTS uq_teams_contest_no;
CREATE UNIQUE INDEX IF NOT EXISTS teams_team_no_key
    ON teams (team_no);

-- 4. 外键
ALTER TABLE audit_logs            DROP CONSTRAINT IF EXISTS fk_audit_logs_contest;
ALTER TABLE import_logs           DROP CONSTRAINT IF EXISTS fk_import_logs_contest;
ALTER TABLE config_snapshots      DROP CONSTRAINT IF EXISTS fk_config_snapshots_contest;
ALTER TABLE screen_config         DROP CONSTRAINT IF EXISTS fk_screen_config_contest;
ALTER TABLE slot_snapshots        DROP CONSTRAINT IF EXISTS fk_slot_snapshots_contest;
ALTER TABLE slot_teams            DROP CONSTRAINT IF EXISTS fk_slot_teams_contest;
ALTER TABLE slots                 DROP CONSTRAINT IF EXISTS fk_slots_contest;
ALTER TABLE seats                 DROP CONSTRAINT IF EXISTS fk_seats_contest;
ALTER TABLE score_change_requests DROP CONSTRAINT IF EXISTS fk_score_change_requests_contest;
ALTER TABLE scores                DROP CONSTRAINT IF EXISTS fk_scores_contest;
ALTER TABLE teams                 DROP CONSTRAINT IF EXISTS fk_teams_contest;
ALTER TABLE tasks                 DROP CONSTRAINT IF EXISTS fk_tasks_contest;
ALTER TABLE events                DROP CONSTRAINT IF EXISTS fk_events_contest;

-- 5. 列
ALTER TABLE audit_logs            DROP COLUMN IF EXISTS contest_id;
ALTER TABLE import_logs           DROP COLUMN IF EXISTS contest_id;
ALTER TABLE config_snapshots      DROP COLUMN IF EXISTS contest_id;
ALTER TABLE screen_config         DROP COLUMN IF EXISTS contest_id;
ALTER TABLE slot_snapshots        DROP COLUMN IF EXISTS contest_id;
ALTER TABLE slot_teams            DROP COLUMN IF EXISTS contest_id;
ALTER TABLE slots                 DROP COLUMN IF EXISTS contest_id;
ALTER TABLE seats                 DROP COLUMN IF EXISTS contest_id;
ALTER TABLE score_change_requests DROP COLUMN IF EXISTS contest_id;
ALTER TABLE scores                DROP COLUMN IF EXISTS contest_id;
ALTER TABLE teams                 DROP COLUMN IF EXISTS contest_id;
ALTER TABLE tasks                 DROP COLUMN IF EXISTS contest_id;
ALTER TABLE events                DROP COLUMN IF EXISTS contest_id;

-- 6. 赛事表最后删（外键已全部解除）
DROP TABLE IF EXISTS contests;

COMMIT;
