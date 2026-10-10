-- 0018 events.phases —— 赛项阶段配置
--
-- 为什么加这一列
--   demo 原型（赛事统分后台管理_demo.html）的赛项配置里有 phases：
--   未来之城 = 自动阶段(120s) + 手动阶段(105s)，平板的计时与时间奖励的基准时长都按它算。
--   但 events 表原先只落了 score_rule / bonus_rules / penalty_rule / rank_rule，没有 phases，
--   于是有两个静默后果：
--     ① 配置经后端保存一次，阶段就被**整个丢掉**（不报错，只是不见了）；
--     ② engine.RefTimeFor 看不到阶段，只能退回 params.refTime / 默认 120s ——
--        而前端 eventTotalSec 是「各阶段时长之和」，未来之城前端 225s、后端 120s，
--        时间奖励因此算出不同的分数（封顶 10 分内可差到 5 分以上）。
--   两者都不报错却直接影响成绩，所以补列，而不是继续在前端绕。
--
-- 为什么用 JSONB 而不是拆成多列
--   阶段属于「赛项规则配置」，字段随赛制演进（timerMode / source / timeBonus…），
--   与 score_rule / rank_rule 同性质。拆列会让每次赛制微调都要发一次迁移。
--
-- 空态与其它 JSONB 列保持一致：NOT NULL + 默认 '[]'，
-- 避免出现 NULL 与 '[]' 两种「空」导致读点要写两种判断。

ALTER TABLE events ADD COLUMN IF NOT EXISTS phases JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN events.phases IS
  '赛项阶段配置 [{id,name,durationSec,timerMode,source,desc,timeBonus}]；'
  '多阶段赛项的时间奖励基准时长 = 各阶段 durationSec 之和（与前端 eventTotalSec 同口径）；'
  '无阶段赛项为空数组';
