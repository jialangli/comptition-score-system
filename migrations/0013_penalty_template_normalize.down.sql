-- ============================================================================
-- 0013 回滚：把「判罚模板归一」撤回。
--
-- ⚠️ **归一不可逆**：被覆盖的原始值（per_card / none）没有留底，
--    down 无法还原"哪条原来是哪个值"。本 down 只把列注释改回，
--    并在下面写明正确的回退姿势。
--
-- 为什么不写「一律改回 per_card」：
--   那等于给一批赛项凭空加上"按牌扣分"的口径（而它们本来是 record_only），
--   比不还更危险 —— 属于"为了可回滚而制造错误数据"。
--
-- 若要真正回退某条赛项：用业务接口改，或直接改 JSONB：
--   UPDATE events SET penalty_rule = jsonb_set(penalty_rule,'{template}','"per_card"')
--    WHERE id = '<赛项 id>';
-- ============================================================================

BEGIN;

COMMENT ON COLUMN events.penalty_rule IS
    '扣分规则，template ∈ record_only|per_card|none';

COMMIT;
