-- ============================================================================
-- 0011：评分方式新增 toggle（是否完成）
--
-- 背景（2026/10/08 产品口径）：后台「赛项与规则配置 → 评分方式」只有三类 ——
--   数值评分 numeric / 计数得分 count / 是否完成 toggle。
--   原「等级评分 enum」已下线（前端不可选），但**历史数据仍要能读**，
--   故 enum 继续留在 CHECK 白名单与本表的列 COMMENT 里。
--
-- 设计要点：
--
--   1. **只放宽约束，不动数据**：tasks.type 是内联 CHECK，Postgres 自动命名为
--      tasks_type_check。但**不硬依赖这个名字** —— 先按「定义里含 numeric」
--      从 pg_constraint 查出实际约束名再删（老库命名若有差异也不会漏），
--      然后统一 ADD 回本仓约定名 tasks_type_check（与 evidence_kind_check 同约定）。
--      已有行一律不受影响。
--
--   2. **enum 保留**：不迁移、不删除既有 enum 任务。前端与 Go 模型都保留了
--      enum 的中文映射与算分分支，专门用于「历史数据只读显示」。
--
--   3. **toggle 的取值语义**：完成记满分（tasks.max_score）、未完成记 0。
--      与 nil（未录入）严格区分 —— 见 engine 包 taskRawScore 的三态语义注释。
-- ============================================================================

BEGIN;

-- 删掉「评分方式」这一条 CHECK（按定义内容匹配，避免依赖自动生成的约束名）
DO $$
DECLARE c record;
BEGIN
  FOR c IN
    SELECT conname
      FROM pg_constraint
     WHERE conrelid = 'tasks'::regclass
       AND contype  = 'c'
       AND pg_get_constraintdef(oid) LIKE '%numeric%'
  LOOP
    EXECUTE format('ALTER TABLE tasks DROP CONSTRAINT %I', c.conname);
  END LOOP;
END $$;

ALTER TABLE tasks ADD CONSTRAINT tasks_type_check
    CHECK (type IN ('numeric','count','enum','toggle'));

COMMENT ON COLUMN tasks.type IS
    'numeric=数值评分 count=计数得分 toggle=是否完成 enum=等级评分（已下线，仅兼容历史数据）';

COMMIT;
