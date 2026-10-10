-- 0016_team_seat_session —— 队伍级归台 + 参赛轮次（2026/10/10）
--
-- 背景：前端原型（后台 demo）的「分台与顺位」以**队伍级归台**为唯一事实源
--       （赛台 + 台内顺位 + 参赛轮次），场次队伍由它**派生**；后端此前只有
--       slot_teams（场次-队伍绑定），teams 表既无 seat_id 也无 session，
--       于是「按队伍参赛轮次判」这类判据在后端**无法表达**。
--
-- 三列语义：
--   seat_id    NULL = 未排台（尚未分台，**不是错误状态**）
--   seat_order 台内顺位，从 1 起；未排台时为 0
--   session    参赛轮次：'1' / '2' / 'both'（默认 both = 两轮都打）
--
-- ⚠️ 外键用 ON DELETE SET NULL，**刻意偏离**本仓「*_id 外键一律 RESTRICT」的约定：
--    赛台是「赛事级资源」，删台是常规编排动作（改场地 / 减台位）。若用 RESTRICT，
--    删一个赛台会被「该台下还有队伍」拦下，运营只能先逐队取消归台 ——
--    而队伍并没有丢，它只是回到「未排台」，删台后重新分台本就是这个流程的预期结果。
--    SET NULL 让数据自己回到合法状态。
--
-- ⚠️ session 用文本而不是整数：'both' 表达「两轮都打」是**正常取值**，
--    不是缺省哨兵。若用 0 或 3 当哨兵，每个读点都要记住哨兵含义，且从响应里
--    看不出「这队是一轮都没设，还是设成了两轮」。取值与前端原型完全一致。

ALTER TABLE teams ADD COLUMN IF NOT EXISTS seat_id    BIGINT;
ALTER TABLE teams ADD COLUMN IF NOT EXISTS seat_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE teams ADD COLUMN IF NOT EXISTS session    TEXT    NOT NULL DEFAULT 'both';

-- 归台外键（先删后建：迁移脚本要可重复执行，直接 ADD CONSTRAINT 第二次会报「已存在」）
ALTER TABLE teams DROP CONSTRAINT IF EXISTS teams_seat_id_fkey;
ALTER TABLE teams ADD CONSTRAINT teams_seat_id_fkey
    FOREIGN KEY (seat_id) REFERENCES seats(id) ON DELETE SET NULL;

-- 参赛轮次取值域
ALTER TABLE teams DROP CONSTRAINT IF EXISTS teams_session_check;
ALTER TABLE teams ADD CONSTRAINT teams_session_check
    CHECK (session IN ('1','2','both'));

-- 派生查询（赛台 × 赛项 × 组别 × 轮次 → 队伍）走这条索引。
-- 用部分索引：未排台的队伍不参与派生，而现场绝大多数队伍都已排台 ——
-- 省下的正是那条不该被扫到的路径（异常态）。
CREATE INDEX IF NOT EXISTS ix_teams_seat ON teams (seat_id, seat_order) WHERE seat_id IS NOT NULL;

COMMENT ON COLUMN teams.seat_id    IS '归台：所属赛台 ID；NULL = 未排台（尚未分台，不是错误状态）';
COMMENT ON COLUMN teams.seat_order IS '台内顺位（从 1 起）；未排台时为 0';
COMMENT ON COLUMN teams.session    IS '参赛轮次：1 / 2 / both（默认 both）；与赛项轮次取交集才是该队真正要打的轮次';
