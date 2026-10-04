# 变更记录

本项目的版本记录 notable changes。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

> **发布状态**：本项目尚未发布稳定版。`main` 为开发分支，功能可能随时变化。
> 历史以 commit 记录为准，本文件从 P6 起开始维护。

## [未发布]

### 新增

#### 后端 P6 · 改分申请单落库

- 新增 `score_change_requests` 表（0003 迁移），改分申请**不再只写审计**
- **防重复提交**：对 `(team_id, round_no)` 建「仅未审批」的部分唯一索引
  `ux_scr_one_pending`。去重放在数据库而非应用层「先查再插」——
  后者在并发下两个请求都会查到「没有」然后都插入
- 新增 `ChangeRequestRepo` 接口与 PostgreSQL 实现
- service 层新增 `ListPendingChanges`（待审批队列）、`PendingChangeOf`、`RejectChange`
- `ApplyScoreChange` 在事务内自动把挂起的申请单标记为已授权（**方法签名未变**）
- 新增错误类型 `ErrChangePending`（已有待审批申请）、`ErrAlreadyDecided`（重复审批）

#### 工程体验

- 新增 `scripts/pg_start.sh` / `pg_stop.sh`：幂等启停，带 `postmaster.pid` 残留自愈，
  启动失败自动打印日志尾部
- 新增 `_goenv.sh`：构建入口，隔离 GOROOT 与沙箱注入的 `HTTP_PROXY`，
  并注入 `-buildvcs=false`（`.git/refs` 缺失时 VCS 打标会失败）

### 变更

#### 奖项分配口径（经评审定调）

- **`awardComplete` 默认改为 `true`**（`GET /events/{id}/standings`）：
  未完成录入的队伍**保留名次但不占获奖名额**。
  修复了「一场没比的队伍也拿三等奖」——基准数据中未来之城的破晓队、智造队（总分 0）不再获奖。
  需查看旧口径时显式传 `?awardComplete=0`
- **奖项名额由 `ceil` 改为 `floor`**，并对每个 `ratio > 0` 的档位**保底 1 个**：
  - `floor` 管住「不超编」：小组赛人数少时不再出现「全员获奖」
  - 保底管住「不空档」：否则 3 队 × 0.1 = 0.3 → 0 个名额，
    会出现「一等奖 3 人、二等奖 0 人、三等奖 0 人」

#### 联网口径澄清

- README 修正两处过时描述：移除已删除的「连接后端 / 离线兜底」与「所有表带 `contest_id`」
- 明确区分：**后台管理端必须联网使用**（不做离线兜底）；平板裁判端另有离线能力

### 测试

- 新增 `TestRankAwardsUseFloorNotCeil`：锁定 `floor` 语义（3 队 × 0.34 只发 1 个，
  `ceil` 会发 2 个）。旧测试用 `ratio = 1.0` 恰好规避了 `floor`/`ceil` 差异，故此前未暴露
- 基准 `testdata/frontend_golden.json` 按新口径重算

### 修复

- `TestEndToEndMainFlow` 现覆盖改分申请落库链路（此前只覆盖写审计）

## [原型 · 14 项需求评审结项]

### 新增

- **#8 赛项模板库 + 勾选建赛**：新建赛事可勾选本次要办的赛项，
  带入完整规则（任务 / 分值 / 计分模板 / 判罚事由 / 争议类型），并按 `eventId` 同步过滤赛程排期
- **#9 分台页「勾选队伍按编号均分」**：按队伍编号排序后 round-robin 铺到各赛台，
  未勾选队伍保持原分台
- **#10 叫号表导出 Excel** / **#13 公示表导出 Excel**：
  零依赖 `buildXlsx` 写出引擎（store 模式 ZIP + inlineStr 单元格 + 正确 CRC32）
- **#4 P5.8d 比赛结束提示视觉放大**：红色告警渐变底 + 脉冲呼吸光晕 + 右侧滑入

### 变更

- **#C 状态机替代手动锁**：由「手动锁成绩」改为「校验通过才允许锁」，
  避免未完成录入被直接锁榜
- **#11 一场赛事一个裁判长**管所有赛项
- **#3 未来之城双队 A/B 完全独立计时**（各记各的）
- **#2 裁判端字号**支持 小 / 中 / 大 三档
- **#1 P2b 改分模式**放开用时可编辑
- **#12 删除奖项**，仅保留名次

### 移除

- **P10 成绩锁定 / 解锁页**（#7）
- 后台的「连接后端」与「离线兜底」整层（后台管理端改为必须联网使用）
