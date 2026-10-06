-- ============================================================================
-- 0008：赛台-队伍可写锁（team_write_locks）
--
-- 背景：E 项此前只做了「冲突**发生后**自动建单」，但没做**预防**。
-- P12 note 7 要求的是源头掐断：
--
--	绑定赛台后，同台同一队伍仅允许一台平板持有可写锁
--	（先提交 / 先暂存者占锁，另一台对该队只读并提示「该队正由 X 执裁」），
--	从源头掐断「两台平板各打一份」。
--
-- 只建单不预防的代价：现场要等补传撞车才发现，而那时两份成绩都已经打完了，
-- 裁判长还得在 P8 里二选一。加锁可以把绝大多数冲突消灭在发生之前。
--
-- 设计要点：
--
--   1. **锁的粒度是（赛台, 队伍），不含轮次**。
--      同一台平板要连续打完该队的第 1 轮和第 2 轮，锁若按轮次划分，
--      打完 R1 就得重新抢锁，而抢锁的间隙正是另一台平板插进来的窗口。
--
--   2. **唯一索引即锁**：UNIQUE(contest_id, seat_id, team_id)。
--      抢占用 INSERT ... ON CONFLICT，靠数据库裁决谁是第一个 ——
--      应用层「先查有没有锁再插」在两台平板同时点提交时，两边都会查到「没有」。
--
--   3. **锁必须有过期时间**（expires_at）。
--      现场平板会没电、会掉线、会被裁判长拿走去干别的，
--      没有 TTL 的锁会把一个队伍**永久锁死** —— 那比不加锁更糟。
--      过期锁在抢占时视为不存在，直接被覆盖。
--
--   4. **只有持锁者能释放**：释放带 `AND holder = $n`。
--      否则 A 正在打分，B 点一下「释放」就把 A 的锁解了，锁形同虚设。
--
--   5. holder 与 holder_label 分开存：holder 是机器标识（clientId，用于判等），
--      holder_label 是给人看的（「该队正由 X 执裁」里的 X）。
--
-- 影响面：纯新增表，不改任何既有表结构，历史数据无需回填。
-- ============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS team_write_locks (
    id           BIGSERIAL   PRIMARY KEY,
    contest_id   TEXT        NOT NULL DEFAULT 'ct_default'
                             REFERENCES contests(id) ON DELETE RESTRICT,
    seat_id      BIGINT      NOT NULL,
    team_id      BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- 持锁者机器标识（clientId）。判等用它，不用展示名 —— 展示名可能重名。
    holder       TEXT        NOT NULL,
    -- 展示名：另一台平板提示「该队正由 X 执裁」里的 X
    holder_label TEXT        NOT NULL DEFAULT '',
    acquired_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 过期时间：到点视为未锁定，避免平板掉线后把队伍永久锁死
    expires_at   TIMESTAMPTZ NOT NULL
);

-- 核心约束即锁本身：同一赛台同一队伍只能有一把锁
CREATE UNIQUE INDEX IF NOT EXISTS ux_team_write_lock
    ON team_write_locks (contest_id, seat_id, team_id);

-- 清理过期锁 / 按持锁者查询
CREATE INDEX IF NOT EXISTS ix_team_write_lock_holder
    ON team_write_locks (contest_id, holder);

COMMENT ON TABLE  team_write_locks             IS '赛台-队伍可写锁：同台同队仅一台平板可写，从源头预防两台平板各打一份';
COMMENT ON COLUMN team_write_locks.holder      IS '持锁者机器标识（clientId）；判等用它，不用展示名';
COMMENT ON COLUMN team_write_locks.holder_label IS '持锁者展示名，用于提示「该队正由 X 执裁」';
COMMENT ON COLUMN team_write_locks.expires_at  IS '过期时间：到点视为未锁定。没有 TTL 的锁会把队伍永久锁死，比不加锁更糟';

COMMIT;
