-- ============================================================================
-- 0006：发布单元与移交/发布状态机（release_units）
--
-- 背景：前端 P11「确认并移交」+ P13「发布状态回流看板」定义了完整的发布链路，
-- 但后端**完全没有承载它的实体**（全仓搜「发布 / 移交 / publish」0 处匹配）。
-- 缺口的后果很具体：裁判长点了「确认并移交」之后，无从知道后台到底发出去没有 ——
-- 这正是流程走查 G 项点名的「现场屏 ≠ 后台真值」黑盒。
--
-- 发布单元的定义来自 P13：**赛项 × 组别 × 赛台**。
-- 不是赛项、也不是整场赛事 —— 一个赛项在三个赛台上就是三个发布单元，
-- 因为它们是分别移交、分别发布的。
--
-- 设计要点：
--
--   1. **状态四态而非布尔**（P13 note 2）：
--        未移交 → 已移交·处理中 → ⏳ 待发布 → ✓ 运营已发布
--      前端两个列（移交状态 / 发布状态）是同一状态的两种视图，
--      因此只存一个 status，标签由 model 层派生，不存两份真值。
--
--   2. **重发用标记而不是第五个状态**（P13 note 5）。
--      已发布之后若发生改分 / 裁定生效，状态回退到「待发布」并置
--      republish_required —— 它与「首次待发布」在后端是同一件事
--      （都是等运营点发布），差别只在前端要不要标红提示。
--      做成第五个状态会让「能不能点发布」的判断多一条分支。
--
--   3. **每个流转都记人与时间**：handed_by/at、received_by/at、published_by/at。
--      发布人是 P13 表格最后一列的内容，也是事后追责的依据。
--
--   4. **发布单元唯一性带赛事维度**：UNIQUE(contest_id, event_id, group_code, seat_id)。
--      同赛项同组别同赛台在同一赛事里只能有一个单元；
--      跨赛事可以重复（A、B 两场赛事各有自己的「赛台 A」）。
--
-- 影响面：纯新增表，不改任何既有表结构，历史数据无需回填。
-- ============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS release_units (
    id           BIGSERIAL   PRIMARY KEY,
    contest_id   TEXT        NOT NULL DEFAULT 'ct_default'
                             REFERENCES contests(id) ON DELETE RESTRICT,
    event_id     TEXT        NOT NULL,                -- 赛项（与 events.id 同域，不建外键：赛项删除时发布单元要留档）
    group_code   TEXT        NOT NULL,                -- 组别
    seat_id      BIGINT      REFERENCES seats(id) ON DELETE SET NULL,
    -- 状态：not_handed 未移交 / handed 已移交·处理中 / pending ⏳ 待发布 / published ✓ 运营已发布
    status       TEXT        NOT NULL DEFAULT 'not_handed'
                             CHECK (status IN ('not_handed', 'handed', 'pending', 'published')),
    handed_by    TEXT        NOT NULL DEFAULT '',     -- 移交人（裁判长）
    handed_at    TIMESTAMPTZ,
    received_by  TEXT        NOT NULL DEFAULT '',     -- 接收人（工作人员）
    received_at  TIMESTAMPTZ,
    published_by TEXT        NOT NULL DEFAULT '',     -- 发布人（运营）
    published_at TIMESTAMPTZ,
    -- 已发布后又有改分 / 裁定生效 → 置真，状态回退为 pending 等重发
    republish_required BOOLEAN NOT NULL DEFAULT false,
    republish_reason   TEXT    NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 发布单元唯一：同赛事下「赛项 × 组别 × 赛台」只能有一个
CREATE UNIQUE INDEX IF NOT EXISTS ux_release_unit
    ON release_units (contest_id, event_id, group_code, COALESCE(seat_id, 0));

-- P13 看板按赛事拉全部单元，并按状态分组计数
CREATE INDEX IF NOT EXISTS ix_release_contest_status
    ON release_units (contest_id, status, event_id);

COMMENT ON TABLE  release_units             IS '发布单元：赛项×组别×赛台，承载移交→发布的状态机（前端 P11 / P13）';
COMMENT ON COLUMN release_units.status      IS 'not_handed 未移交 / handed 已移交·处理中 / pending 待发布 / published 已发布';
COMMENT ON COLUMN release_units.republish_required IS '已发布后又发生改分或裁定生效 → 需重发；状态同时回退为 pending';
COMMENT ON COLUMN release_units.event_id    IS '刻意不建外键：赛项被删时发布单元要留档，不能跟着消失';
COMMENT ON COLUMN release_units.seat_id     IS '赛台删除时置空（SET NULL）：赛台可能临时撤并，但发布记录要保留';

COMMIT;
