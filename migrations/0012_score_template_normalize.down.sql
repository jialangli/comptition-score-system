-- ============================================================================
-- 0012 回滚：把「计分规则归一」撤回。
--
-- ⚠️ **归一不可逆**：被覆盖的原始值（weighted_sum / average）没有留底，
--    down 无法还原"哪条原来是哪个值"。本 down 只把列注释改回，
--    并在下面写明正确的回退姿势。
--
-- 为什么不写「一律改回 weighted_sum」：
--   那等于凭空把一批赛项的算分口径改掉（而它们绝大多数本来就是 sum），
--   比不还更危险 —— 属于"为了可回滚而制造错误数据"。
--
-- 若要真正回退某条赛项：用业务接口改，或直接改 JSONB：
--   UPDATE events SET score_rule = jsonb_set(score_rule,'{template}','"weighted_sum"')
--    WHERE id = '<赛事 id>';
-- ============================================================================

BEGIN;

COMMENT ON COLUMN events.score_rule IS
    '计分模板，template ∈ weighted_sum|sum|average';

COMMIT;
