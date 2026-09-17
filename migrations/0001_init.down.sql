-- ============================================================================
-- 回滚 0001_init
-- 顺序：先删依赖方，再删被依赖方（外键 RESTRICT 要求如此）
-- 注意：此脚本会删除全部业务数据，仅用于开发环境重建
-- ============================================================================

BEGIN;

-- 触发器与函数
DROP TRIGGER IF EXISTS trg_events_touch ON events;
DROP TRIGGER IF EXISTS trg_teams_touch  ON teams;
DROP TRIGGER IF EXISTS trg_scores_touch ON scores;
DROP FUNCTION IF EXISTS touch_updated_at();

-- 业务表（从叶子到根）
DROP TABLE IF EXISTS screen_config;
DROP TABLE IF EXISTS config_snapshots;
DROP TABLE IF EXISTS import_logs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS slot_snapshots;
DROP TABLE IF EXISTS slot_teams;
DROP TABLE IF EXISTS slots;
DROP TABLE IF EXISTS seats;
DROP TABLE IF EXISTS scores;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS events;

COMMIT;
