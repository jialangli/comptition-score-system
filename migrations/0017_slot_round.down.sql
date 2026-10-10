-- 0017 回滚：撤掉场次轮次列。
--
-- 注意：`slot_teams` 的写入路径已在同批改造中停用（`POST /slots/{id}/teams` 返回 410），
-- 场次队伍改为「队伍级归台派生」。本列回滚后，服务端将无法派生场次队伍 ——
-- 回滚到旧版本时请连代码一起回滚（旧代码读的是 slot_teams，不看本列）。

ALTER TABLE slots DROP CONSTRAINT IF EXISTS slots_round_no_check;
ALTER TABLE slots DROP COLUMN IF EXISTS round_no;
