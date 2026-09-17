-- 回滚 0002：移除审批人列。
-- 注意：列一旦删除，已记录的审批人信息不可恢复 —— 回滚前请先导出 audit_logs。

BEGIN;

DROP INDEX IF EXISTS ix_audit_approver;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS approver;

COMMIT;
