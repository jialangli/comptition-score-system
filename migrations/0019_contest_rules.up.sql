-- 0019 contest_rules —— 赛事级规则（首个落库的是「名次编排 / 递补」）
--
-- 为什么要有这张表
--   前端 `S.substituteRule = {mode, note}` 是**赛事级**配置（不是赛项级，也不是赛台级）：
--     mode='none'  不递补（**默认**）：被裁定「取消资格」的队伍留下的名次空缺**保留**，
--                  后续队伍保留原名次 —— 公示表上会出现 4 → 6 这种空洞
--     mode='rank'  按名次顺延：空缺不补，后续队伍名次前移（连续编号）
--   后端 engine.Rank 原先只有「连续编号」一种行为（= 等于恒定递补），
--   于是同一场比赛：前端公示表第 5 名空缺、后端说第 5 名另有其人 —— 对不上。
--
-- 为什么单独一张表，而不是给 contests 加两列
--   contests 存的是**身份与表头**（名称 / 赛季 / 起止 / 场馆 / 主办 / 状态 / 归档），
--   而这里存的是**规则**。前端也是分开的：`substituteRule` 与 `screen` 平级挂在赛事数据空间上，
--   后端已经按同一映射把 `screen` 落在独立的 `screen_config` 表。这里沿用该约定，
--   后续再有赛事级规则（而非身份信息）就往本表加列，不要塞进 contests。
--
-- 一行一赛事；**没有行 = 全部取默认值**（mode='none'）。
--   不给每个赛事预插一行：默认值必须只有一处定义（model.SubstituteMode 的兜底），
--   预插一行等于把默认值复制到数据里，改了代码也改不动存量数据。

CREATE TABLE IF NOT EXISTS contest_rules (
    contest_id       TEXT        PRIMARY KEY,
    substitute_mode  TEXT        NOT NULL DEFAULT 'none',
    substitute_note  TEXT        NOT NULL DEFAULT '',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_contest_rules_contest
        FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT,
    CONSTRAINT contest_rules_substitute_mode_check
        CHECK (substitute_mode IN ('none', 'rank'))
);

COMMENT ON TABLE contest_rules IS
    '赛事级规则（一行一赛事）。无记录 = 取代码里的默认值，不要靠预插数据表达默认。';
COMMENT ON COLUMN contest_rules.substitute_mode IS
    '名次编排：none = 不递补（默认，取消资格队的位置留空、后续队伍保留原名次）；'
    'rank = 按名次顺延（空缺不补，连续编号）。与前端 substituteRule.mode 逐字一致。';
COMMENT ON COLUMN contest_rules.substitute_note IS
    '为什么这么设 —— 切换递补规则会改公示名次，必须能回答"当时是谁、基于什么改的"';
