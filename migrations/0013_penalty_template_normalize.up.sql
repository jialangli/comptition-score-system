-- ============================================================================
-- 0013：判罚模板归一 —— 数据层只留「仅记录不扣分」（record_only）
--
-- 背景（2026/10/09 口径）：
--   界面早已只读显示「仅记录不扣分」，但库里若还留着历史值，就会被如实显示成
--   「按牌扣分」—— 用户实际就看到了这个（那个赛项的 penalty_rule.template 是 per_card）。
--   本迁移把数据归一，让「显示」与「数据」一致。
--
-- 影响评估：
--   本赛制的判罚口径是「只作记录与留痕、不扣分」，per_card（按牌扣分）从未真正使用，
--   所以对现有数据**预期是空操作**。
--
--   ⚠️ 唯一的实质影响：若某历史赛项**真的**配成过 per_card 并且已录成绩，归一后重算将
--      **不再扣分**，该赛项的总分会变高。若你不确定库里有没有这种赛项，先跑一遍这句看看：
--        SELECT id, name, updated_at FROM events WHERE penalty_rule->>'template' = 'per_card';
--      查出来是空就放心执行；非空则先确认这些赛项的成绩是否需要保留。
--
-- 语义：template 缺键或为空 → 补成 record_only。
-- 幂等：重复执行不产生变化。
--
-- ⚠️ 同时刷新 updated_at：增量同步游标基于它，不刷新就等于"库改了、前端没改"。
--
-- 注：penalty_rule.params 里的 per_card 参数（yellow / red）**保留不动** ——
--    它们是通用参数字段，清掉没有好处。engine 的 per_card 算分分支同样保留
--    （golden 逐位比对要用），只是"库里不会再出现这种值"。
-- ============================================================================

BEGIN;

UPDATE events
   SET penalty_rule = jsonb_set(penalty_rule, '{template}', '"record_only"', true),
       updated_at = now()
 WHERE COALESCE(penalty_rule->>'template', '') <> 'record_only';

COMMENT ON COLUMN events.penalty_rule IS
    '判罚规则；本赛制 template 恒为 record_only（仅记录不扣分），历史值已由迁移 0013 归一';

COMMIT;
