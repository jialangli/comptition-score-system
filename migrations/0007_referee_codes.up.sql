-- ============================================================================
-- 0007：裁判码（referee_codes）
--
-- 背景：前端 P1 家族的登录链路完全建立在裁判码上 —— 姓名 + 6 位裁判码双因子，
-- 校验通过后按后台预绑的「赛项 × 组别 × 赛台」自动绑定执裁范围（P1.5 / P1.6）。
-- 后端此前对此**零支持**（测试里的「裁判A」只是 operator 字符串）。
--
-- 关键口径（逐条来自 wireframe）：
--
--   1. **姓名 + 裁判码双因子**：只验码不够。校验失败要能**区分两种原因** ——
--      码无效（P1.5b）与姓名不匹配（P1.5c）是两个独立页面，
--      所以后端必须给出不同语义的错误，不能统一成「登录失败」。
--
--   2. **执裁范围后台预绑，不在登录时选择**。赛项 / 组别 / 赛台 / 身份
--      （裁判长 vs 普通裁判）全部在建档时写入，登录只做校验并原样返回。
--      现场可互换平板，但执裁范围固定 —— 错评由实际签字裁判负责。
--
--   3. **首登联网激活，之后断网可登**（P1.5 note 3）：
--      激活成功 → 凭据缓存本机 → 此后断网也能登录。
--      从未激活的新平板离线首登会被拒绝 —— 这条由**平板端**判定，
--      后端只需在激活时把绑定信息一次性返回，并在库里标记已激活。
--
--   4. **裁判码是赛事级凭证**：换赛事必须重新建档发码，旧码在新赛事无效。
--      落地为 contest_id 分区 + UNIQUE(contest_id, code) ——
--      同一个码可以在不同赛事各发一次，但查码永远只在当前赛事内查。
--
--   5. **码本身不存明文之外的东西**：不存密码、不存 token。
--      本期鉴权仍是插槽（api.CurrentUser），裁判码只做「这个人是谁、
--      执裁范围是什么」的解析，不签发会话。
--
-- 影响面：纯新增表，不改任何既有表结构，历史数据无需回填。
-- ============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS referee_codes (
    id          BIGSERIAL   PRIMARY KEY,
    contest_id  TEXT        NOT NULL DEFAULT 'ct_default'
                            REFERENCES contests(id) ON DELETE RESTRICT,
    code        TEXT        NOT NULL,               -- 6 位无歧义字符，区分大小写
    name        TEXT        NOT NULL,               -- 裁判姓名（双因子的另一半）
    -- 身份：referee 普通裁判 / chief 裁判长。登录成功后据此分流到 P2 或 P7
    role        TEXT        NOT NULL DEFAULT 'referee'
                            CHECK (role IN ('referee', 'chief')),
    -- 执裁范围：后台建档时预绑，登录时只读返回
    event_id    TEXT        NOT NULL DEFAULT '',
    group_code  TEXT        NOT NULL DEFAULT '',
    seat_id     BIGINT      REFERENCES seats(id) ON DELETE SET NULL,
    -- 激活态：unused 建档未用 / activated 已激活（首登联网激活后置为它）
    status      TEXT        NOT NULL DEFAULT 'unused'
                            CHECK (status IN ('unused', 'activated', 'revoked')),
    activated_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 同一赛事内裁判码唯一；跨赛事可重复（赛事级凭证）
CREATE UNIQUE INDEX IF NOT EXISTS ux_referee_code
    ON referee_codes (contest_id, code);

-- 后台按赛事列出全部裁判码（含激活状态）
CREATE INDEX IF NOT EXISTS ix_referee_contest
    ON referee_codes (contest_id, created_at);

COMMENT ON TABLE  referee_codes            IS '裁判码：姓名+6位码双因子，预绑执裁范围，赛事级凭证';
COMMENT ON COLUMN referee_codes.code       IS '6 位无歧义字符（不含 0/O/1/I/L），区分大小写；生成见 service 层';
COMMENT ON COLUMN referee_codes.role       IS 'referee 普通裁判（登录后进 P2）/ chief 裁判长（进 P7）';
COMMENT ON COLUMN referee_codes.event_id   IS '预绑赛项；与 group_code / seat_id 共同构成执裁范围';
COMMENT ON COLUMN referee_codes.status     IS 'unused 建档未用 / activated 已激活（首登联网后）/ revoked 已作废';

COMMIT;
