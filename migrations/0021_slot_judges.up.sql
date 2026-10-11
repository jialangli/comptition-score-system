-- 0021：场次加「执裁排班」（slots.judges）
--
-- 为什么加：demo 的排班页（执裁排班 / 一键按预绑排班）把裁判指派存在 slot.judges
-- （裁判档案 ID 数组）里，后端 slots 表此前**没有对应列** —— 排班读写接口（图集 8 号
-- 缺口）没有落库位置，demo 与后端各自为政。
--
-- 建模取舍：直接在 slots 上挂 JSONB 数组，而不是另开 slot_judges 关系表：
--   1. 排班是场次的**附属属性**，没有脱离场次独立查询的场景（读永远随场次列表一起回）；
--   2. 数组保序 = 指派顺序有语义（队列表「执裁」一列按序渲染）；
--   3. 与 events.phases / tasks.unit 同一手法：规则性数据落 JSONB，
--      合法性交 service 校验（裁判必须在本赛事建档、已作废不能排、去重保序）。
--
-- 元素是 referee_codes.id（bigint）而非码值：码是登录凭证、可重发变更，
-- 排班引用的是「档案」这个人，主键引用才稳定。
--
-- 默认 '[]'：存量场次 = 未排班（不是「数据缺失」），读侧无需特判 NULL。
ALTER TABLE slots ADD COLUMN IF NOT EXISTS judges JSONB NOT NULL DEFAULT '[]';

COMMENT ON COLUMN slots.judges IS
    '本场次执裁裁判（referee_codes.id 数组，按指派顺序）；空 = 未排班';
