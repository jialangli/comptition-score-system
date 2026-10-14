-- ============================================================================
-- 0022：改分申请单加「用时」（before_time / after_time）
--
-- 背景：wireframe P2b「用时（可改 · 改分模式专属）」要求改分模式下可修正用时，
-- 且「须填原因并随本单审批、原值由计时器带出」。但 0003 的申请单只存了总分
-- （before_total / after_total），用时无处安放 —— 而用时既是**时间奖励**的计算
-- 输入，又是**并列裁决的 tiebreak**（总分 ↓ → 用时 ↑ → 轮次小者）。
--
-- 于是出现一条绕过审批的缝：POST apply-change 的 body 本来就带 time（直接写
-- scores.duration_sec），于是「改时」能落库、能影响名次，却**不在申请单里、
-- 也不在审计 diff 里** —— 审批人核对的是总分，落库的却是另一组数。
--
-- 本迁移补上这两列，让「改时」和「改分」走同一条申请 → 审批 → 留痕的链：
--   - before_time：申请时的原用时（秒），**由服务端从成绩记录带出**，不信前端；
--   - after_time ：申请拟改成的新用时（秒）；
--   - 两列都可为 NULL：**保持向后兼容** —— 只改分不改时的申请照旧不填，
--     既有历史单（0003 之后已落库的）也天然为 NULL，语义是「本单不涉及用时」。
--
-- 为什么用 DOUBLE PRECISION 而不是 NUMERIC：与 scores.duration_sec 同类型，
-- 避免两处算分路径上的隐式转换；用时是浮点秒（前端可给 02:41.5），不是金额。
--
-- 为什么不为两列加 CHECK(>=0)：校验放在 service（错误信息能带字段名与中文提示），
-- 数据库层只守 NOT NULL / 唯一性这类结构性约束，避免同一口径两处各写一遍。
--
-- 影响面：纯新增可空列，不改既有列、不重建索引，历史数据无需回填。
-- ============================================================================

BEGIN;

ALTER TABLE score_change_requests
    ADD COLUMN IF NOT EXISTS before_time DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS after_time  DOUBLE PRECISION;

COMMENT ON COLUMN score_change_requests.before_time IS
    '申请时的原用时（秒），服务端从 scores.duration_sec 带出；NULL=本单不涉及用时';
COMMENT ON COLUMN score_change_requests.after_time IS
    '申请拟改成的新用时（秒）；NULL=本单不涉及用时。裁判长授权落库时，提交的 time 必须与此值一致';

COMMIT;
