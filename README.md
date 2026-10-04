# NeuroMaster 赛事评分系统 · 产品原型与后端

[![CI](https://github.com/jialangli/comptition-score-system/actions/workflows/ci.yml/badge.svg)](https://github.com/jialangli/comptition-score-system/actions/workflows/ci.yml)
[![规范检查](https://github.com/jialangli/comptition-score-system/actions/workflows/convention.yml/badge.svg)](https://github.com/jialangli/comptition-score-system/actions/workflows/convention.yml)
[![Go](https://img.shields.io/badge/Go-1.25.0-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PR](https://img.shields.io/badge/PR-welcome-success.svg)](CONTRIBUTING.md)

BrainCo（强脑科技）NeuroMaster 赛事评分系统：**可交互产品原型（单文件 HTML）+ Go 后端**。
本仓库用于需求评审、交互验证与后端开发。

> 仓库内所有队伍、学校、成绩均为**虚构演示数据**，仅保存在浏览器本地 localStorage，不会上传到任何服务器。

**在线体验**：[后台管理 demo](https://jialangli.github.io/comptition-score-system/%E8%B5%9B%E4%BA%8B%E7%BB%9F%E5%88%86%E5%90%8E%E5%8F%B0%E7%AE%A1%E7%90%86_demo.html) · [裁判端 wireframe](https://jialangli.github.io/comptition-score-system/%E8%A3%81%E5%88%A4%E6%89%93%E5%88%86%E7%B3%BB%E7%BB%9F_wireframe.html)

## 目录

- [在线预览](#在线预览github-pages) · [仓库结构](#仓库结构) · [功能模块](#后台管理-demo--功能模块) · [裁判端](#裁判打分-wireframe平板端)
- [技术特点](#技术特点) · [数据与隐私](#数据与隐私) · [开发](#开发)
- [参与贡献](#参与贡献) · [文档](#文档) · [致谢](#致谢) · [许可](#许可)

## 在线预览（GitHub Pages）

| 交付物 | 预览 |
|---|---|
| 赛事统分后台管理 demo（多赛事版 · 主交付物） | [打开](https://jialangli.github.io/comptition-score-system/%E8%B5%9B%E4%BA%8B%E7%BB%9F%E5%88%86%E5%90%8E%E5%8F%B0%E7%AE%A1%E7%90%86_demo.html) |
| 裁判打分系统 wireframe（平板端 · 30 页） | [打开](https://jialangli.github.io/comptition-score-system/%E8%A3%81%E5%88%A4%E6%89%93%E5%88%86%E7%B3%BB%E7%BB%9F_wireframe.html) |
| 早期单页版原型（保留作对比） | [打开](https://jialangli.github.io/comptition-score-system/) |

也可以直接把对应 `.html` 下载到本地双击打开 —— 单文件、零依赖、离线可运行。

## 仓库结构

```
赛事统分后台管理_demo.html   后台管理端交互原型（多赛事版 · 单文件 · 零依赖）
裁判打分系统_wireframe.html  平板端裁判打分 wireframe（30 页单屏 UI）
index.html                   早期单页版原型（保留作对比）

cmd/                         进程装配（main）
internal/
  engine/                    纯函数计算：计分 / 排名 / 脱敏 / 校验（零 IO）
  model/                     领域模型
  store/                     存储契约（repo.go）
  store/postgres/            PostgreSQL 实现（pgx/v5）
  service/                   用例编排、事务边界、审计埋点
  api/                       HTTP 处理器、路由、中间件
migrations/                  0001~0003 迁移（up / down 成对）
scripts/                     pg_start / pg_stop / test_db / smoke / env
testdata/                    前端基准数据（frontend_golden.json）
web/                         内嵌前端资源（embed）

ARCHITECTURE.md              分层设计与关键决策
PROGRESS.md                  实施进度与验证记录
CONTRIBUTING.md              贡献指南
CHANGELOG.md                 变更记录
_goenv.sh                    构建入口（隔离 GOROOT 与沙箱代理）
```

## 后台管理 demo · 功能模块

侧栏结构：**总览（置顶）／赛事／赛前准备／赛中／赛后与合规**，共 14 项。

| 分组 | 模块 | 说明 |
|---|---|---|
| 总览 | **总览** | 开赛就绪度、赛项状态、待办公示 |
| 赛事 | **赛事管理** | 一场赛事一份独立数据空间；状态四态（筹备中 / 进行中 / 已结束 / 已归档）；归档真只读 |
| 赛事 | **跨赛事** | 并行总控（跨场次只读汇总）+ 跨赛事汇总榜（成绩对照，不做总分排名） |
| 赛前准备 | **赛项与规则** | 赛项 / 任务 / 评分方式（数值 · 计数 · 等级 · 是否完成）/ 算分模板（直接求和 · 加权 · 均值）；配置快照与回滚 |
| 赛前准备 | **赛台与赛程** | 赛台数量可配；赛台 × 时段编排；就近自动分配 + 手动改派 |
| 赛前准备 | **队伍与报名** | 队伍管理（弃赛软删除 / 改组）+ WRC 报名导入（自研 `.xlsx` 解析，变更比对后合并入库） |
| 赛前准备 | **裁判与排班** | 裁判档案、6 位无歧义裁判码 + 赛台绑定（赛事级凭证）、执裁排班 |
| 赛中 | **裁判打分** | 按评分方式自动渲染控件；用时 / 黄红牌；选手签字；改分申请（需裁判长授权） |
| 赛中 | **实时榜单** | 两轮取优 → 加权计分 → 加分 → 扣牌 → 排名 → 奖项分配 |
| 赛中 | **大屏展示** | 分页轮播；姓名脱敏；置顶锁屏 + 锁定赛事（后台切场带不走） |
| 赛中 | **处理进度看板** | 告警收件箱（自批 / 解锁 / 取消资格 / 超时）+ 改分 / 争议台账 —— **纯只读**，审批裁定仍在裁判长平板端 |
| 赛后与合规 | **锁定与发布** | 成绩锁 + 配置锁双锁；解锁必填原因并生成告警；双锁就位才可发布；含资格与递补（取消资格登记 + 递补预览） |
| 赛后与合规 | **留底证据库** | 成绩 / 工单 / 裁定的证据留存与补传 |
| 赛后与合规 | **合规** | 角色权限矩阵 + 全操作审计（旧值 → 新值 + 原因，越权尝试同样留痕） |

## 裁判打分 wireframe（平板端）

- **30 页平板单屏 UI**：打分录入、改分审批（P9）、争议裁定台（P8）、锁定台（P10）、选手签字等完整现场链路
- 未来之城 V7.3 **双队对抗同场**模式；裁判码 6 位无歧义字符，首次登录联网激活后**断网可登录**，支撑离线评分
- 后台 demo 的双锁 / 告警 / 台账口径与平板端同源（P8 / P9 / P10 note 逐条对齐）

## 技术特点

### 原型（单文件 · 零依赖）

- **单文件交付**：HTML + CSS + 原生 JavaScript 全部内联，**无构建、无第三方库**，
  下载后双击即用，裁判现场平板无需任何环境
- **多赛事架构**：一场赛事一份独立数据空间（存储 key 隔离）；赛事索引合并式写入 + 删除墓碑；
  「当前赛事」按浏览器标签独立（总部一台电脑统管多场）
- **零第三方 `.xlsx` 引擎**：解析（ZIP central directory + `DecompressionStream('deflate-raw')` + XML 扫描）
  与写出（store 模式 ZIP + inlineStr + CRC32）全部自研，从源头消除解析库的攻击面
- **自研脱敏**：姓名脱敏在双端逐位一致，避免公示表泄露选手信息

### 后端（Go · 局域网单二进制）

- **技术栈**：net/http + pgx/v5 + PostgreSQL 17，**仅 2 个直接依赖**
- **分层铁律**：`engine`（纯函数计算，零 IO）／`store`（存储契约）／`service`（事务与编排）／`api`（HTTP）
- **审计不可绕过**：凡改变业务数据的操作，必须与其审计记录写在同一事务
- **SQL 注入免疫**：全部走 pgx 参数绑定
- **计分双端逐位一致**：Go 引擎与前端 JS 引擎由 `testdata/frontend_golden.json` + 一组
  `TestParityWithFrontend*` 测试锁定，任何一侧改动都必须同步另一侧

## 数据与隐私

- 全部数据为**虚构演示数据**，不代表任何真实赛事
- Demo 数据仅存浏览器本地 localStorage，清除浏览器数据即恢复内置种子，也可用页顶「重置种子」
- **后台管理端为「必须联网使用」**：不做离线兜底、不做后端双模式。
  平板裁判端另有离线能力（裁判码首登激活后凭据缓存本机），两者口径不同、不可混用

## 开发

```bash
# 环境（Go 1.25.0）
source scripts/env.sh

# 启动依赖（幂等）
bash scripts/pg_start.sh
bash scripts/test_db.sh

# 跑测试（-p 1 必加，详见 CONTRIBUTING.md）
export TEST_DATABASE_URL="postgres://postgres@127.0.0.1:5432/neuroscore_test?sslmode=disable"
export TEST_DATABASE_URL_API="postgres://postgres@127.0.0.1:5432/neuroscore_test_api?sslmode=disable"
go test -p 1 ./...
```

## 参与贡献

请先阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。本仓库有两类贡献，改动前请注意：

- **原型**是单文件 CRLF 交付物，改动需带 `count` 断言 + 三道校验（详见贡献指南「原型改动约定」）
- **计分 / 排名 / 脱敏是双实现**（浏览器 JS + Go 引擎），改一侧必须同步另一侧并更新基准
- ⛔ **本仓库禁用 `git stash`**（会导致 `.git/refs` 消失），暂存改动请用文件复制

## 文档

| 文档 | 内容 |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 分层设计、数据流、关键决策与取舍 |
| [PROGRESS.md](PROGRESS.md) | 实施进度（按 P1~P6 分阶段）、验证记录、已知问题与待办 |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 贡献指南与改动约定 |
| [SECURITY.md](SECURITY.md) | 安全策略与漏洞报告方式 |
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | 社区行为准则 |
| [CHANGELOG.md](CHANGELOG.md) | 变更记录 |

## 致谢

本项目参考了 [FIRST Robotics Competition](https://www.firstinspires.org/)、
[VEX Robotics](https://www.vexrobotics.com/) 等机器人竞赛的赛事组织与评分实践，
其「裁判独立计分、成绩可追溯、赛制配置化」的理念对本项目影响深远。

## 许可

[MIT](LICENSE) © 2026 ljlang

