-- 0017_slot_round —— 场次轮次（时段 ↔ 轮次绑定）
--
-- 背景：队伍级归台定案后，**场次队伍由「赛台 × 赛项 × 组别 × 轮次」派生**。
-- 前三项 slots 表都有，唯独缺「轮次」—— 后端于是算不出某场次该有哪些队伍
-- （平板端「本赛台队列」也就一直没有权威数据源）。
--
-- 口径（与前端 addSlot 一致）：**上午 = 第 1 轮、下午 = 第 2 轮**。
--
-- 为什么必须**落列**、不能由 period 现推：
--   赛项只有 1 轮时，那一场完全可能被排在下午（赛程安排问题，不是赛制问题）。
--   若按 period 现推，该场次会被当成「第 2 轮」，标了「只打第 1 轮」的队伍
--   就会在这张台上凭空消失。
--
-- 回填只认时段（period='下午' → 2，其余 → 1）：
--   迁移期无从得知赛项计划 —— 本项目 events 表**没有轮次字段**
--   （前端是 expectedRounds(ev) 现算的，后端取不到）。
--   因此 1 轮赛项的下午场次需要运营在「赛台与赛程」页人工确认。
--   自查（列出被回填成第 2 轮的场次，逐条核对是否属于 2 轮赛项）：
--     SELECT sl.id, sl.period, sl.event_id, sl.group_code FROM slots sl
--     WHERE sl.round_no = 2 ORDER BY sl.event_id, sl.seat_id;
--
-- ⚠️ 派生判据不看「赛项计划几轮」，只看**场次轮次 vs 队伍参赛轮次**
--   （model.TeamSession.Includes）：session='both' 两轮都命中、'1' 只命中第 1 轮。
--   赛项只有 1 轮时自然不存在 round_no=2 的场次，标了「只打第 2 轮」的队伍就是一场都不打 ——
--   这与前端「赛项轮次 ∩ 参赛轮次 = 空」同义，不需要额外判断赛项配置。

ALTER TABLE slots ADD COLUMN IF NOT EXISTS round_no SMALLINT NOT NULL DEFAULT 1;

-- 回填（幂等：重复执行结果相同）
UPDATE slots SET round_no = 2 WHERE period = '下午' AND round_no <> 2;

ALTER TABLE slots DROP CONSTRAINT IF EXISTS slots_round_no_check;
ALTER TABLE slots ADD CONSTRAINT slots_round_no_check CHECK (round_no BETWEEN 1 AND 2);

COMMENT ON COLUMN slots.round_no IS
  '轮次（1/2）：上午=第 1 轮、下午=第 2 轮；派生场次队伍时与队伍参赛轮次比对';
