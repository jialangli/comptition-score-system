-- ============================================================================
-- 0002：审计日志增加「审批人」列
--
-- 背景：需求确认单明确要求改分时记录「修改前后的分数、**操作人及审批人**」。
-- 0001 只有 operator 一列，审批人只能塞进 reason 文本里 —— 那样无法按审批人
-- 检索（「这个月裁判长批了几次改分」就答不上来），也无法做数据校验。
--
-- 影响面：仅新增一列且带默认值，历史数据自动为空串，无需回填。
-- ============================================================================

BEGIN;

ALTER TABLE audit_logs
    ADD COLUMN IF NOT EXISTS approver TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN audit_logs.operator IS '实际操作人：谁做了这次改动';
COMMENT ON COLUMN audit_logs.approver IS
    '审批人（裁判长）；仅需授权才能执行的操作填写，其余为空';

-- 「按审批人查」是审计的常见入口，建个索引
CREATE INDEX IF NOT EXISTS ix_audit_approver ON audit_logs (approver) WHERE approver <> '';

COMMIT;
