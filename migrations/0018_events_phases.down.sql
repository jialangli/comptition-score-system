-- 0018 回滚：移除 events.phases。
--
-- ⚠️ 回滚会**丢失**已保存的阶段配置（未来之城的自动 / 手动阶段时长），
-- 而时间奖励的基准时长会退回 params.refTime → 默认 120s。
-- 回滚前先导出赛项配置留底，或确认这些赛项当前没有多阶段配置。

ALTER TABLE events DROP COLUMN IF EXISTS phases;
