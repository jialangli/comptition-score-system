-- ============================================================================
-- 0022 down：撤掉改分申请单的「用时」两列
--
-- 回滚即丢数据：已落库的 before_time / after_time 会一并消失，而申请单的其他字段
-- （总分 / 原因 / 审批）不受影响。若这批改动已经上过现场、单子里有用时值，
-- 回滚前先把两列导出留档（SELECT id, before_time, after_time FROM score_change_requests）。
--
-- 幂等：DROP COLUMN IF EXISTS，重复执行不报错。
-- ============================================================================

BEGIN;

ALTER TABLE score_change_requests
    DROP COLUMN IF EXISTS before_time,
    DROP COLUMN IF EXISTS after_time;

COMMIT;
