# 赛事统分后台管理系统 · 后端架构设计

> 技术栈：Go（net/http + pgx/v5）+ PostgreSQL 17
> 定位：局域网部署、离线优先、单二进制交付
> 状态：P1 ✅ ｜ P2 ✅ ｜ P3 ✅ ｜ P4（API 层 + HTTP 测试 + 冒烟脚本）✅ ｜ P5 进行中 ｜ 详见 `PROGRESS.md`

---

## 0. 一句话定位

把现有 `index.html` 单文件原型里用 localStorage 承载的所有业务数据，搬到 **PostgreSQL**，由 **Go 服务**统一提供 REST API；前端增加「连接后端」模式，断网时回落 localStorage，联网后批量上行同步。

---

## 1. 技术选型与理由

| 选择 | 理由 |
|---|---|
| **标准库 `net/http`**，不引 Gin/Echo | 本项目路由简单（~40 个端点），标准库 1.22+ 的 `ServeMux` 已支持方法+路径参数；少一层框架依赖，部署更轻 |
| **`pgx/v5`** 而非 `database/sql` + lib/pq | 原生支持 PG 的 JSONB、批量插入、连接池；性能与类型映射更好 |
| **不引 ORM**，手写 SQL | 计分/排名涉及聚合与窗口函数，ORM 反而碍事；SQL 全在 store 层可见，便于 DBA 复核 |
| **`embed` 内嵌前端** | 编译产物是**单个 exe**，前端也打包进去 —— 契合赛场「拷过去就能跑」的部署方式 |
| **`modernc.org/sqlite`（仅测试）** | 让 `go test` 无需真实 PG 实例即可跑（可选，见 §9） |

---

## 2. 目录结构（职责划分）

```
comptition-score-server/
├── cmd/
│   └── server/
│       └── main.go                  # 唯一入口：读配置 → 装依赖 → 启动 HTTP → 优雅退出
│
├── internal/                        # internal 防止被外部模块 import（Go 惯例）
│   ├── config/
│   │   └── config.go                # 环境变量 / .env 解析（DSN、端口、上传目录、dev 开关）
│   │
│   ├── model/                       # 领域模型：只有结构体、常量、枚举。零依赖
│   │   ├── event.go                 # Event / Task / ScoreRule / BonusRule / PenaltyRule / RankRule
│   │   ├── team.go                  # Team（编号、名称、学校、教练、组别、状态、来源）
│   │   ├── score.go                 # ScoreRecord（轮次、各任务值、用时、黄红牌、签字）
│   │   ├── schedule.go              # Seat / Slot / SlotSnapshot（加时赛场内快照）
│   │   ├── audit.go                 # AuditLog / ImportLog
│   │   └── screen.go                # ScreenConfig（每屏条数、停留秒数、锁定态）
│   │
│   ├── engine/                      # ★ 纯函数业务规则：不碰 DB、不碰 HTTP → 100% 可单测
│   │   ├── scoring.go               # 任务分 → 基础分 → 加分 → 扣分 → 总分（含两轮取优）
│   │   ├── ranking.go               # 排序 + 同分裁决 + 奖项按比例分配
│   │   ├── validate.go              # 配置校验（权重和、id 唯一、enumMap 完整、奖励引用）
│   │   └── mask.go                  # 姓名脱敏（大屏 / 对外公示）
│   │
│   ├── store/                       # 存储层：只管 SQL，返回 model
│   │   ├── store.go                 # 接口定义（EventStore / TeamStore / ScoreStore …）
│   │   ├── errors.go                # ErrNotFound / ErrConflict 等语义化错误
│   │   ├── postgres/
│   │   │   ├── db.go                # 连接池、WithTx 事务 helper、健康检查
│   │   │   ├── event.go
│   │   │   ├── team.go
│   │   │   ├── score.go
│   │   │   ├── schedule.go
│   │   │   ├── audit.go
│   │   │   └── screen.go
│   │   └── sqlite/                  # 可选：测试用实现（与 postgres 同接口）
│   │
│   ├── service/                     # 用例编排：事务边界 + 审计埋点，不含 SQL
│   │   ├── event_service.go         # 赛项增删改查、配置校验、快照生成/回滚
│   │   ├── team_service.go          # 队伍 CRUD、弃赛/恢复（软删除）、改组
│   │   ├── import_service.go        # 报名导入：解析 → 列映射 → 校验 → 变更比对 → 入库
│   │   ├── score_service.go         # 成绩录入、改分申请、两轮取优
│   │   ├── schedule_service.go      # 赛台配置、场次编排、就近分配、加时赛快照
│   │   ├── rank_service.go          # 榜单计算、公示/成绩表导出数据
│   │   ├── screen_service.go        # 大屏分页数据（脱敏）
│   │   └── audit_service.go         # 统一留痕入口
│   │
│   ├── api/                         # HTTP 层：只做参数解析 / 校验 / 序列化
│   │   ├── router.go                # 路由注册表（~40 个端点 + 404 JSON 兜底）
│   │   ├── middleware.go            # 恢复 panic、请求日志、CORS（dev）、当前用户中间件
│   │   ├── response.go              # 统一响应封装 + 错误码映射
│   │   ├── params.go                # 路径 / 查询参数解析 helper
│   │   ├── health.go                # /healthz
│   │   ├── event_handler.go         # 赛项 / 校验 / 配置快照 / 回滚
│   │   ├── team_handler.go          # 队伍 CRUD / 弃赛 / 恢复
│   │   ├── import_handler.go        # 报名导入预览 / 提交（含 multipart 文件上传）
│   │   ├── score_handler.go         # 录分 / 改分申请 / 授权改分
│   │   ├── schedule_handler.go      # 赛台 / 场次 / 自动分配 / 加时赛快照
│   │   ├── rank_handler.go          # 榜单 / 公示表 / 成绩表
│   │   ├── screen_handler.go        # 大屏配置 / 分页轮播
│   │   ├── audit_handler.go         # 审计日志 / 导入日志 / 审计概览
│   │   ├── sync_handler.go          # 离线批量上行
│   │   ├── api_test.go              # HTTP 层集成测试（httptest + 真实 PG）
│   │   └── errors_test.go           # 错误映射单元测试
│   │
│   └── xlsx/                        # 报名表解析（Go 版，对齐前端已实现的逻辑）
│       ├── reader.go                # ZIP + deflate + XML 扫描（与前端同算法）
│       └── reader_test.go
│
├── migrations/
│   ├── 0001_init.up.sql
│   └── 0001_init.down.sql
│
├── web/                             # 前端静态资源（内嵌进二进制）
│   └── index.html                   # 现有 demo，增加「连接后端」适配层
│
├── scripts/
│   ├── pg_init.sh                   # 初始化本地 PG 数据目录
│   ├── pg_start.sh / pg_stop.sh     # 启停本地 PG
│   └── dev.sh                       # 一键：起 PG + 跑服务（dev 模式）
│
├── testdata/
│   ├── wrc_sample.xlsx              # 报名表样例
│   └── events_seed.json             # 三赛项配置种子
│
├── go.mod / go.sum
└── README.md
```

---

## 3. 分层依赖规则（严格单向）

```
        cmd/server
             │
             ▼
    ┌────── api ──────┐
    │                 │
    ▼                 ▼
 service ──────► engine        （engine 被 service 调用，也可被 api 直接调做校验）
    │
    ▼
  store
    │
    ▼
  model  ◄── 所有层都可依赖它，它不依赖任何人
```

**铁律**

1. `model` 不 import 任何内部包
2. `engine` 只 import `model`（纯函数，无 IO）→ 这是**可单测的核心资产**
3. `store` 不 import `service`/`api`
4. `api` 不写业务逻辑、不直接调 `store`（必须过 `service`）
5. SQL 只允许出现在 `store/postgres` 下

---

## 4. 领域模型与数据库表

| 表 | 说明 |
|---|---|
| `events` | 赛项：id / name / groups / score_rule / bonus_rules / penalty_rule / rank_rule / custom_formula（规则部分存 **JSONB**，字段名与前端 Schema v1 完全一致） |
| `tasks` | 任务项：event_id / id / name / type / max_score / weight / control / enum_map |
| `teams` | 队伍：**event_id / team_no（唯一）/ name / school / coach / group_code / status(active/withdrawn) / source(excel/manual/api)** |
| `scores` | 打分记录：team_id / round_no / task_values(JSONB) / duration_sec / yellow / red / signed / operator / created_at |
| `seats` | 赛台：event_id / name / sort_order |
| `slots` | 场次：seat_id / period / time_range / event_id / group_code / **type(normal/extra)** |
| `slot_teams` | 场次-队伍 关联（正式场次队伍来源主库） |
| `slot_snapshots` | **加时赛场内快照**：slot_id / team_no / name / school / coach —— 独立表，**不写主库** |
| `audit_logs` | 六类操作留痕：operator / action / target / before / after / reason / created_at |
| `import_logs` | 报名导入审计：source / operator / summary(JSONB) / detail(JSONB) / status |
| `config_snapshots` | 配置快照：note / events(JSONB) / created_at |
| `screen_config` | 大屏配置：event_id / page_size / interval_sec / pinned |

**关键约束**

- `teams(event_id, team_no)` 唯一索引 —— 落实「一号一队」强校验
- `audit_logs` 建 `created_at` 索引，保留 ≥2 年（迁移脚本里附归档说明）
- 所有 `*_id` 外键加 `ON DELETE RESTRICT`（避免误删带成绩的队伍）

---

## 5. API 设计（前缀 `/api/v1`）

| 资源 | 端点 |
|---|---|
| 赛项 | `GET/POST /events` · `GET/PUT/DELETE /events/{id}` · `POST /events/{id}/validate` |
| 配置快照 | `GET /events/{id}/snapshots` · `POST /events/{id}/snapshots` · `POST /events/{id}/snapshots/{sid}/restore` |
| 队伍 | `GET/POST /events/{id}/teams` · `PUT/DELETE /teams/{id}` · `POST /teams/{id}/withdraw` · `POST /teams/{id}/restore` |
| 报名导入 | `POST /imports/preview`（返回列映射 + 四色变更清单，不落库）· `POST /imports/commit` |
| 赛台赛程 | `GET/POST /seats` · `GET/POST/PUT/DELETE /slots` · `POST /slots/{id}/auto-assign` · `POST /slots/{id}/snapshot` |
| 打分 | `GET/PUT /teams/{id}/scores` · `POST /scores/{id}/change-requests` |
| 榜单 | `GET /events/{id}/standings?group=` |
| 大屏 | `GET /screen/{eventId}`（**已脱敏**，返回当前屏数据 + 分页元信息）· `PUT /screen/{eventId}/config` |
| 导出 | `GET /events/{id}/export/public`（公示表，仅排名）· `GET /events/{id}/export/detail`（成绩表，含明细） |
| 审计 | `GET /audit-logs` · `GET /import-logs` |
| 同步 | `POST /sync`（离线批量上行，带客户端 `lastSyncAt`） |
| 健康 | `GET /healthz`（返回 DB 连通性 + 版本） |

**统一响应格式**

```json
{ "code": 0, "message": "ok", "data": { } }
```

错误用 `AppError{HTTPStatus, Code, Message}` 在 api 层统一映射，避免各 handler 各写一套。

---

## 6. 关键设计决策

1. **计分引擎搬进 Go 并保持纯函数** —— 前端那套 `computeTotal`（任务分 → 两轮取优 → 加分 → 扣分 → 排名 → 奖项）在 Go 里重写，输入输出全是结构体，**表驱动单测**覆盖三个赛项 + 边界（空值/缺项/扣分超标）。
2. **前后端同源** —— Go 直接 serve `web/`，不留 CORS 问题；开发时用 `-dev` 从磁盘读，改前端刷新即生效。
3. **离线优先是双向的** —— 前端继续用 localStorage 兜底；后端提供 `POST /sync` 做批量上行，前端记录 `lastSyncAt` 做增量。
4. **审计由 service 层统一埋点** —— 不散落在 handler 里，保证「六类操作」漏不掉；审计与业务写在**同一事务**，不会出现「改了但没留痕」。
5. **规则用 JSONB，字段名照抄 Schema v1** —— 前端、导出、后端三处字段完全一致，省掉一层映射代码。
6. **加时赛快照独立表** —— 物理隔离，从表结构上杜绝「快照污染主库」。
7. **不做鉴权（本期）** —— 但在 `middleware.go` 预留 `currentUser(ctx)` 插槽，下一期接入裁判码登录时不用改 handler 签名。
8. **单二进制交付** —— `go build` 出一个 exe，前端已 embed；赛场上拷到机器、配好 DSN 就能跑。
9. **迁移脚本手写、顺序编号** —— 不引 migrate 框架，`0001_init.up.sql` 直接可读，便于学校/公司 DBA 审。
10. **时间统一 UTC 存储、展示层转本地** —— 避免跨时区赛事（美国/北京赛区）出现排序错乱。

---

## 7. 与前端 / 原型的兼容约定

| 约定 | 说明 |
|---|---|
| 字段命名 | 一律 **camelCase**，与现有 `index.html` 内的对象结构逐字一致 |
| 枚举值 | 内部仍用英文键（`numeric` / `weighted_sum` / `score,time`），中文只出现在界面层 —— 后端不做翻译 |
| 计分结果 | 后端返回 `{base, bonus, penalty, total, complete}`，与前端 `computeTotal` 返回结构一致 |
| 变更比对 | `POST /imports/preview` 返回 `insert/update/skip/conflict` 四态，与前端一致 |
| 大屏数据 | 后端直接返回**已脱敏**的姓名（`程**`），避免前端漏脱敏 |

---

## 8. 分阶段实施计划

| 阶段 | 内容 | 可验证标准 |
|---|---|---|
| **P1** ✅ | 项目骨架 + config + model + migrations + healthz | `go build` 通过；`/healthz` 返回 DB ok |
| **P2** ✅ | engine 计分/排名/校验/脱敏 + 表驱动单测 | `go test ./internal/engine/...` 全绿，三赛项算例与前端结果一致（engine 覆盖率 100%） |
| **P3** ✅ | store/postgres CRUD + service 编排 + 审计埋点 | 集成测试：建赛项 → 导队伍 → 录成绩 → 出榜单，审计有 6 类记录（含「审计写失败则业务回滚」的不变式验证） |
| **P4** ✅ | api 层全部端点 + 统一错误映射 + HTTP 测试 + 冒烟脚本 | `go test ./internal/api/...` 全绿；`bash scripts/smoke.sh` 跑通 11 段断言 |
| **P5** ✅ | 前端接后端（连接开关 + 后端加载 + 成绩上行 + 赛项/队伍/赛程反向同步 + lastSyncAt + 离线兜底 + embed 单 exe） | 浏览器打开 → 切后端模式 → 数据来自 PG；断网仍可操作；生产构建单 exe 交付 |
| **P6** | 报名导入（xlsx）+ 公示/成绩表导出 | 用真实 WRC 样例 xlsx 走完四态比对 |

---

## 9. 测试策略

| 层 | 手段 | 说明 |
|---|---|---|
| `engine` | 表驱动单测 | 覆盖率目标 ≥ 90%，纯函数无 mock |
| `store` | 集成测试（需真实 PG） | 用 `testdata` 建临时库，跑完清理 |
| `service` | 集成测试 | 断言「业务结果 + 审计记录」双写一致 |
| `api` | `httptest` + 真实 PG | handler 层，不依赖网络；与 service 测试使用**不同测试库**，避免并行踩踏 |
| 端到端 | `scripts/smoke.sh` | curl 串联全流程，CI 可跑 |

**测试库隔离**：`go test ./...` 默认并行执行不同包。`service` 包默认连接 `neuroscore_test`，`api` 包默认连接 `neuroscore_test_api`，由 `scripts/test_db.sh` 统一重建并迁移。

> 若本机无 PG 实例，`store/sqlite` 实现可让 `go test ./...` 在无外部依赖下跑通（可选，优先保证 postgres 为主实现）。

---

## 10. 明确不做的（Non-goals）

- ❌ 本期不做鉴权 / 权限（预留插槽，下一期）
- ❌ 不做 WebSocket 实时推送（大屏用 5 秒轮询，够用且简单）
- ❌ 不做附件 zip 归档的异步任务队列（先同步打包，量大再上 worker）
- ❌ 不引入 ORM、不引入 Web 框架、不引入前端构建工具
