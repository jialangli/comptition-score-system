-- 0016 回滚：撤掉队伍级归台与参赛轮次三列。
--
-- 说明：回滚**丢失**归台与参赛轮次数据（这是仅存于这三列的信息，
-- 场次队伍绑定 slot_teams 仍独立保留，不受影响）。
-- 回滚前若要留档，先导出：
--   SELECT id, event_id, team_no, seat_id, seat_order, session FROM teams WHERE seat_id IS NOT NULL;

DROP INDEX IF EXISTS ix_teams_seat;

ALTER TABLE teams DROP CONSTRAINT IF EXISTS teams_session_check;
ALTER TABLE teams DROP CONSTRAINT IF EXISTS teams_seat_id_fkey;

ALTER TABLE teams DROP COLUMN IF EXISTS session;
ALTER TABLE teams DROP COLUMN IF EXISTS seat_order;
ALTER TABLE teams DROP COLUMN IF EXISTS seat_id;
