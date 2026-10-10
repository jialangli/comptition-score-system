-- 0015_slot_rematch —— 场次类型增加「重赛」（2026/10/10 产品定）
--
-- 背景：现场存在「某队因器材故障 / 受干扰等原因需要重打一次」的场景，此前系统里**没有任何表达方式**
--       （全仓 `重赛` 只在两处说明文字里被提及，其中一处还明确写「不是重赛」）。
-- 方案（用户拍板）：**重赛 = 赛项内的独立场次，复用加时赛机制** ——
--       队伍以场内快照导入、不写入队伍主库、不参与分台派生、归档时独立标记。
--       与 extra 的区别只在「为什么打」：extra 用于影响冠亚季军 / 晋级；rematch 用于某队重打一次。
-- 成绩采用口径：**不自动进榜单**（它不参与分台派生）；若要把它作为榜单成绩，
--       仍走「改分申请 → 审批」（有留痕），不新增第二条改成绩路径。
--
-- 本迁移只扩 slots.slot_type 的取值域。列级 CHECK 由 Postgres 自动命名，
-- 名称已实测确认为 slots_slot_type_check，故 drop 后重建。

ALTER TABLE slots DROP CONSTRAINT IF EXISTS slots_slot_type_check;
ALTER TABLE slots ADD CONSTRAINT slots_slot_type_check
    CHECK (slot_type IN ('normal', 'extra', 'rematch'));

COMMENT ON COLUMN slots.slot_type IS
    'normal=正式场次（队伍来自主库、参与分台派生）/ extra=加时赛（独立场次，影响冠亚季军或晋级时启用）/ rematch=重赛（独立场次，某队因故重打一次）；extra 与 rematch 均以场内快照为准、不写队伍主库';
