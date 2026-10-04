-- ============================================================================
-- 0003：改分申请单落库（score_change_requests）
--
-- 背景：需求确认单要求「裁判提交后禁止直接改分，需发起修改请求；裁判长授权
-- 后方可修改」。P3 只把「申请」写进了审计日志 —— 审计能回答「谁申请过改分」，
-- 但回答不了两个运营问题：
--
--   1. 这个申请批了没有？（审计里没有状态，只能靠文本模糊匹配）
--   2. 同一份申请被重复提交怎么办？（没有去重键，裁判连点两次就有两条）
--
-- 因此补一张申请单表，让「申请 → 授权」成为一个有状态、可查询、可去重的实体。
--
-- 设计要点：
--   - **防重复靠数据库，不靠应用层 if**：对 (team_id, round_no) 建「仅未审批」
--     的部分唯一索引。同一队同一轮同时只能存在一条待审批申请，重复提交会被
--     唯一约束挡下，经 mapError(23505) 转成 store.ErrDuplicate 交给上层拦截。
--   - **已审批的历史申请不去重**：裁判长批完一次后，若确需再改，应能再次发起。
--     所以唯一索引带 WHERE NOT approved。
--   - approved / approver / decided_at 三件一起记，与 audit_logs 的审批人列呼应：
--     审计回答「谁批的」，本表回答「批的是哪一单、什么时候批的」。
--
-- 影响面：纯新增表，不改任何既有表结构，历史数据无需回填。
-- ============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS score_change_requests (
    id           BIGSERIAL   PRIMARY KEY,
    score_id     BIGINT      NOT NULL,                        -- 关联的打分记录（冗余，便于追溯）
    team_id      BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    round_no     SMALLINT    NOT NULL CHECK (round_no BETWEEN 1 AND 2),
    before_total NUMERIC     NOT NULL,                        -- 申请时的原总分
    after_total  NUMERIC     NOT NULL,                        -- 申请改成的新总分
    reason       TEXT        NOT NULL,                        -- 必填：申诉 / 复核原因
    operator     TEXT        NOT NULL DEFAULT '',             -- 申请人（裁判）
    approved     BOOLEAN     NOT NULL DEFAULT false,          -- 是否已获裁判长授权
    approver     TEXT        NOT NULL DEFAULT '',             -- 审批人
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ                                  -- 授权/驳回时间；待审批时为 NULL
);

-- 核心约束：同一队同一轮，最多一条「待审批」申请。
-- 重复提交在这里被挡住，而不是靠应用层先查再插（那样有竞态窗口）。
CREATE UNIQUE INDEX IF NOT EXISTS ux_scr_one_pending
    ON score_change_requests (team_id, round_no)
    WHERE NOT approved;

-- 待审批队列的查询入口：按申请时间正序（先到先审）
CREATE INDEX IF NOT EXISTS ix_scr_pending_created
    ON score_change_requests (created_at)
    WHERE NOT approved;

COMMENT ON TABLE  score_change_requests            IS '改分申请单：裁判发起、裁判长授权，全程留痕';
COMMENT ON COLUMN score_change_requests.score_id     IS '发起申请时关联的 scores.id，仅作追溯用，不建外键（成绩可被覆盖）';
COMMENT ON COLUMN score_change_requests.before_total IS '申请时的原总分（引擎算出，非手填）';
COMMENT ON COLUMN score_change_requests.after_total  IS '申请改成的新总分';
COMMENT ON COLUMN score_change_requests.approved     IS '是否已授权；false=待审批，true=已处理';
COMMENT ON COLUMN score_change_requests.decided_at   IS '裁判长处理时间；待审批时为 NULL';

COMMIT;
