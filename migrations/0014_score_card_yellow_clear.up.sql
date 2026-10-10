-- 0014_score_card_yellow_clear —— 黄牌口径变更（2026/10/10 产品定）
--
-- 新口径：黄牌**记满阈值即自动升级 1 张红牌并清零黄牌计数**（循环计数），
--         因此裁判端黄牌计数器的上限 = 阈值，计数器上永远不会出现「阈值 + 1」。
-- 旧口径：yellow 为不封顶的累计值，红牌数由 floor(yellow / 阈值) 现算。
--
-- 本次迁移做两件事：
--   1) 加 upgraded_red 列 —— 已升级出的红牌数必须**落到字段**，因为清零后 floor(yellow/th) 恒为 0，算不出来了；
--   2) 把历史 yellow（可能是 >= 阈值的累计值）归一成 (yellow % th, upgraded_red += yellow / th)。
--      阈值逐赛项不同（events.penalty_rule.cardRules.redThreshold，缺省 3）；关闭黄牌计数器的赛项不动。
--
-- ⚠️ 归一不可逆：原「累计黄牌数」不再单独保留（要追溯请看操作审计里的逐次记牌记录）。
-- ⚠️ 迁移后自查（应返回 0 行 —— 即已没有 >= 阈值的黄牌计数）：
--    SELECT s.id FROM scores s
--    JOIN teams t ON t.id = s.team_id
--    JOIN events e ON e.id = t.event_id
--    WHERE s.yellow >= COALESCE(NULLIF(e.penalty_rule #>> '{cardRules,redThreshold}', '')::int, 3);

ALTER TABLE scores ADD COLUMN IF NOT EXISTS upgraded_red SMALLINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN scores.upgraded_red IS '其中由黄牌累计升级出的红牌数；yellow 为当前黄牌计数（0..阈值-1）';
COMMENT ON COLUMN scores.yellow       IS '黄牌计数（当前周期）；记满阈值即清零并把红牌数 +1';

UPDATE scores s
SET upgraded_red = s.upgraded_red + (s.yellow / th.v),
    yellow       = s.yellow % th.v
FROM (
    SELECT t.id AS team_id,
           COALESCE(NULLIF(e.penalty_rule #>> '{cardRules,redThreshold}', '')::int, 3) AS v
    FROM teams t
    JOIN events e ON e.id = t.event_id
    WHERE COALESCE(e.penalty_rule #>> '{cardRules,enabled}', 'true') <> 'false'
) th
WHERE s.team_id = th.team_id
  AND th.v > 0
  AND s.yellow >= th.v;
