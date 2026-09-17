# 后端实施进度

> 技术栈：Go 1.25.0 + PostgreSQL 17.5
> 项目路径：`D:\Desktop\workbuddy\comptition-score-server\`

---

## 环境（全部用户态，无需管理员权限）

| 组件 | 版本 | 位置 | 备注 |
|---|---|---|---|
| Go 工具链 | go1.25.0 windows/amd64 | `C:\Users\ljl20\.workbuddy\binaries\go\versions\go` | 便携版解压；官方源在国内极慢，**最终走阿里云镜像**下载 |
| GOPATH | — | `C:\Users\ljl20\.workbuddy\binaries\go\gopath` | 系统默认指向 `C:\Program Files\Go` 无写权限，已在 `scripts/env.sh` 覆盖 |
| PostgreSQL | 17.5 | `D:\Desktop\workbuddy\pgsql` | EDB 免安装版解压（**只解压 bin/lib/share**，跳过 pgAdmin/StackBuilder/doc —— 全量解压 2.2 万文件会被系统杀掉） |
| PG 数据目录 | — | `D:\Desktop\workbuddy\pgdata` | `initdb -A trust -E UTF8 --locale=C`，监听 127.0.0.1:5432 |
| 业务库 | — | `neuroscore` | 已执行 `0001_init`，12 张表就绪 |

**日常开发这样起环境：**
```bash
# 1) 启动数据库（若未运行）
D:/Desktop/workbuddy/pgsql/bin/pg_ctl -D "D:/Desktop/workbuddy/pgdata" \
  -l "D:/Desktop/workbuddy/pgdata/server.log" -o "-p 5432" start

# 2) 载入环境变量并运行服务
cd D:/Desktop/workbuddy/comptition-score-server
source scripts/env.sh
go run ./cmd/server
```

---

## P1 已完成：项目骨架 + 配置 + 数据模型 + 迁移 + 健康检查

### 交付文件（19 个）

```
comptition-score-server/
├── ARCHITECTURE.md              架构设计（定稿）
├── PROGRESS.md                  本文件
├── go.mod / go.sum              依赖：pgx/v5 v5.11.0 + x/text v0.29.0（中文排序）
├── cmd/server/main.go           入口：装配依赖、优雅退出
├── internal/
│   ├── config/config.go         环境变量配置 + DSN 打码
│   ├── model/                   领域模型（零依赖）
│   │   ├── event.go             赛项 / 任务 / 计分规则 / 奖项 + FieldError
│   │   ├── team.go              队伍 + 一号一队校验
│   │   ├── score.go             两轮打分记录 + 计分结果
│   │   ├── schedule.go          赛台 / 场次 / 加时赛快照
│   │   ├── audit.go             六类操作留痕 + 导入日志
│   │   └── screen.go            大屏配置（每屏 10 条 / 停留 60 秒）
│   ├── store/
│   │   ├── store.go             存储接口 + 语义化错误
│   │   └── postgres/db.go       连接池 + PG 错误码翻译
│   └── api/
│       ├── router.go            路由注册
│       ├── middleware.go        Recover / 日志 / CORS / 鉴权插槽
│       ├── response.go          统一响应 + 错误映射
│       └── health.go            /healthz
├── migrations/
│   ├── 0001_init.up.sql         12 张表 + 唯一索引 + 触发器
│   └── 0001_init.down.sql       回滚
└── scripts/env.sh               开发环境变量（不污染系统）
```

### 数据库表（12 张）

`events` · `tasks` · `teams` · `scores` · `seats` · `slots` · `slot_teams` ·
`slot_snapshots`（加时赛快照，物理隔离）· `audit_logs` · `import_logs` ·
`config_snapshots` · `screen_config`

关键约束已生效：
- `ux_teams_event_no` 唯一索引 → 落实**「一号一队」**
- `ux_scores_team_round` 唯一索引 → 同队同轮只能一条记录
- 外键一律 `RESTRICT` → 带成绩的队伍无法被误删
- `trg_events_touch` / `trg_teams_touch` / `trg_scores_touch` → `updated_at` 自动维护

---

## 验证记录（真实执行，非声明）

| 检查项 | 命令 | 结果 |
|---|---|---|
| Go 编译 | `go build ./...` | ✅ BUILD_OK |
| 静态检查 | `go vet ./...` | ✅ VET_OK |
| 依赖解析 | `go mod tidy` | ✅ pgx/v5 v5.11.0 |
| 数据库连通 | `pg_isready -h 127.0.0.1 -p 5432` | ✅ accepting connections |
| 迁移执行 | `psql -f migrations/0001_init.up.sql` | ✅ 12 张表创建成功 |
| 约束校验 | `\d teams` / 查 pg_trigger | ✅ 唯一索引 + CHECK + 3 个触发器 |
| 服务启动 | `./bin/score-server.exe` | ✅ 日志：数据库连接成功 / HTTP 已监听 :8080 |
| 健康检查 | `curl /api/v1/healthz` | ✅ `{"code":0,"status":"ok","database":"ok"}` |

服务实际响应：
```json
{"code":0,"message":"ok",
 "data":{"status":"ok","database":"ok","version":"dev","uptimeSec":17,"time":"2026-09-17T12:13:51+08:00"}}
```

---

## P2 已完成：计分引擎（纯函数，零 IO）

### 交付文件

```
internal/engine/
├── scoring.go       三类任务原语取值 → 基础分 → 加分 → 扣分 → 总分；两轮取优
├── ranking.go       排序 + 同分裁决 + 奖项按比例分配 + 按组别独立排名
├── validate.go      配置校验（error 阻断 / warning 提醒两级）
├── mask.go          姓名脱敏（大屏 / 对外公示）
├── collate.go       中文拼音序比较器（与前端 localeCompare('zh') 对齐）
└── *_test.go        5 个测试文件

testdata/frontend_golden.json       前端对照基准（自动生成，勿手改）
scripts/gen_frontend_golden.js      基准生成脚本
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **运算顺序与前端逐位对齐** | Go 侧刻意保持与 JS 完全相同的浮点运算顺序，因此比对用的是**严格相等**而非 epsilon。任何一处顺序差异都会立刻断言失败 |
| **`numOr0` 复刻 `Number(x)\|\|0`** | nil / 空串 / 非法串 / 布尔 / 单元素数组 全部按 JS 语义收敛，避免「同一份数据前端算一套、后端算一套」 |
| **三态语义** | `nil`（未录入）与 `0`（录入了、得 0 分）严格区分 —— `complete` 判定依赖它 |
| **未知计分模板不静默降级** | 前端此处会落入 average 分支算出错误成绩；Go 侧 base 保持 0 并由 `ValidateEvent` 报错阻断 |
| **奖项顺序显式定义** | Go 的 map 迭代顺序随机，直接遍历会让奖项分配「每次刷新都不一样」。标准奖项按档位序、自定义奖项按占比降序 |
| **拼音序而非码点序** | 前端用 `localeCompare(zh)`。实测「破晓队」排在「智造队」前（pò < zhì），码点序相反。**赛事早期大量队伍无成绩（总分 0、用时 0），先后完全由该比较器决定** |
| **中文排序器加锁复用** | `x/text` 的 `Collator.CompareString` 内部复用游标，**非并发安全**（与 `bytes.Compare` 不同）。已用互斥锁串行化并配并发对称性测试 |

### 验证记录

```
go build ./...            → BUILD_OK
go vet ./...              → VET_OK
gofmt -l ./cmd ./internal → 无输出（全部规范）
go test ./...             → ok
go test ./internal/engine -cover → coverage: 100.0% of statements
node scripts/gen_frontend_golden.js → 基准生成成功（11 支队伍 / 3 赛项）
```

**与前端逐位比对结果（P2 验收标准）**

| 比对项 | 结果 |
|---|---|
| 单轮计分 `computeTotal` | **11 例，0 例不一致**（浮点严格相等） |
| 排名 `standings` | 3 个赛项全部一致（名次 / 总分 / 分项 / 用时 / 奖项 / 并列标记） |
| 脱敏 `maskPerson` / `maskMembers` | 16 例全部一致 |
| 配置校验 | 种子三赛项两端均 0 error |

比对方式：`scripts/gen_frontend_golden.js` 从 demo HTML 里**原样抽取**前端实现（不是复制一份代码，否则前端改了这里不会跟着变），跑出期望值 → Go 单测读取并断言。前端一旦改算法，重跑脚本 + `go test` 就能立刻发现两端分叉。

### 与前端的三处有意差异（均为修正，已在代码注释中标注）

1. **无效计分模板**：前端静默落入 average 分支 → Go 侧报 error 阻断保存
2. **`count_bonus` 引用未声明任务**：前端的检查恒真（`some` 遍历必然包含自身），该 error 永不触发 → Go 侧改为 warning，如实说明「派生计数项不计入基础分」
3. **取最优的确定性**：前端 `Object.keys` 依赖插入顺序 → Go 侧显式定义奖项顺序

---

## P3 已完成：存储层 + 服务编排 + 集成测试

### 交付文件

```
internal/store/
├── repo.go                 各业务域 Repo 接口 + Repos 聚合 + 审计筛选条件
└── postgres/
    ├── tx.go               querier 抽象（连接池与事务共用一套 Repo）+ WithTx
    ├── scan.go             JSONB 扫描 / ErrNoRows 翻译 / 参数归一化
    ├── event.go            赛项 + 任务项（整体替换）
    ├── team.go             队伍 CRUD + Upsert（合并式，绝不物理删）
    ├── score.go            两轮成绩 upsert + 按赛项一次性取全量
    ├── schedule.go         赛台 / 场次 / 加时赛快照
    ├── audit.go            审计 + 导入日志
    ├── screen.go           大屏配置 + 配置快照
    └── admin.go            TruncateAll（集成测试与本地重置用）

internal/service/
├── service.go              事务入口、当前用户（鉴权插槽）、语义化错误
├── audit_service.go        统一留痕入口 log / logApproved
├── event_service.go        建/改/删赛项 + 配置快照与回滚
├── team_service.go         队伍 CRUD + 报名导入（预览 / 四色比对 / 分事务入库）
├── score_service.go        录分 / 改分申请 / 授权改分
├── rank_service.go         榜单（按组别）+ 大屏分页与脱敏
└── schedule_service.go     赛台赛程 / 就近自动分配 / 加时赛快照

scripts/test_db.sh          重建并迁移集成测试库
migrations/0002_audit_approver.up.sql   审计增加「审批人」独立列
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **`querier` 抽象** | 同一套 Repo 实现既能跑在连接池上也能跑在事务里。业务代码写法完全一致，不必区分「事务版」和「非事务版」仓储 |
| **`Repos` 是结构体而非接口** | 测试里可以把某一个 Repo 换成「注定失败」的装饰器，从而**构造出**「审计写失败」的场景。这条不变式靠人工 review 保不住（见下方验证） |
| **审计由 `log()` 单点写入** | 全包唯一的留痕写法，强制在 `s.tx(...)` 闭包内调用。想漏痕就得绕开这个函数，而不是「忘了加一行」 |
| **审批人独立成列**（0002 迁移） | 需求要求改分记录「操作人及审批人」。塞进 reason 文本就查不了「裁判长这个月批了几次」，因此加了 `approver` 列 + 部分索引 |
| **导入按「行号」勾选** | 同编号可能出现多行（那正是「编号重复」这种冲突），用编号定位勾选项根本区分不开 |
| **服务端重算比对结果** | 提交时重新预览，只采纳客户端勾选的行号。预览与提交之间可能隔了几分钟，可能已被别人导过一次 |
| **未知模板 / 非法配置先校验再落库** | `CreateEvent` / `UpdateEvent` 先过 `engine.ValidateEvent`，有 error 直接返回 `ValidationError`，不写库 |
| **改配置先留档再改** | 旧配置全量进 `config_snapshots`，字段级差异进审计的 before/after；回滚前再留一份当前状态，让**回滚本身也可回滚** |

### 验证记录（真实执行）

```
go build ./... / go vet ./... / gofmt -l   → 全部通过
go test ./...                              → engine ok / service ok
engine 覆盖率                               → 100.0% of statements
service 覆盖率                              → 82.0%（剩余多为事务闭包里的 return err 分支）
bash scripts/test_db.sh                     → 12 张表 + 0002 迁移就绪
```

**6 个集成测试用例（全部打到真实 PG，无 mock）**

| 用例 | 验证内容 |
|---|---|
| `TestSeedParityThroughDatabase` | 前端基准种子经 service→PG→回读→engine，**榜单与前端原型逐位一致**（3 个赛项全部通过）。一次性压住 JSONB/NUMERIC 往返精度、SQL 映射、取数聚合、引擎计算四层 |
| `TestEndToEndMainFlow` | 建赛项→改配置留档→校验拦截→导队伍(四色)→两轮录分取优→禁改已签字→改分申请→授权改分→弃赛/恢复/改组/删队→赛台调派，并断言**七类操作全部留痕** |
| `TestAuditFailureRollsBackBusiness` | 注入「审计写必失败」的仓储，断言**业务改动一起回滚** —— 这是「改了但没留痕」的唯一真实防线 |
| `TestExtraSlotSnapshotDoesNotPolluteMainTable` | 加时赛快照去重、主库队伍数不变、正式场次与加时赛场次两条路互斥 |
| `TestScreenLockAndPagination` | 分页夹取、越界页回落、锁定本场生效 |
| `TestConfigSnapshotRestore` | 改坏的配置能回滚，且回滚前自动留档 |

外加 18 个覆盖测试函数覆盖幂等、缺参、非法组别、删除残留引用、无变化不留噪音等分支。

**审计链路的真实数据**（直接查库）

| 操作 | 真实痕迹 |
|---|---|
| 改分链路 | `未录入 → 总分 103.8（首次录入）` → `总分 103.8 → 总分 99.0（申请修改，待裁判长授权）` → `总分 103.8 → 总分 106.8`，**审批人 = 裁判长C（独立列）** |
| 六类操作 | 改配置 2 / 改分 4 / 弃赛 2 / 改组 1 / 调赛台 1 / 删队 1（+ 导入队伍 2） |
| 导入审计 | `{insert:3, conflict:2}` → `{skip:1, update:1}`，与预览四色完全一致 |

### 发现并修掉的两个设计缺陷

1. **导入勾选用「队伍编号」定位不成立**：同一份文件里两条同编号的行（正是冲突场景）会被一起命中。改为按**行号**定位，并在 `ImportDiffRow` 上显式暴露 `line` 字段。
2. **审批人只能塞进 reason 文本**：无法按审批人检索，与需求「记录操作人及审批人」不符。补 `0002` 迁移加独立列 + 部分索引，并在 `AuditFilter` 上开放按审批人筛选。

---

## P4 已完成：API 层 + HTTP 测试 + 冒烟脚本

### 交付文件

```
internal/api/
├── router.go              路由注册表（~40 个端点，含 404 JSON 兜底）
├── middleware.go          Recover / 请求日志 / CORS / 操作者中间件
├── response.go            统一响应 + AppError 映射
├── health.go              /healthz
├── event_handler.go       赛项 / 校验 / 配置快照 / 回滚
├── team_handler.go        队伍 CRUD / 弃赛 / 恢复
├── import_handler.go      报名导入预览 / 提交（含 multipart 文件上传）
├── score_handler.go       录分 / 改分申请 / 授权改分
├── schedule_handler.go    赛台 / 场次 / 自动分配 / 加时赛快照
├── rank_handler.go        榜单 / 公示表 / 成绩表
├── screen_handler.go      大屏配置 / 分页轮播
├── audit_handler.go       审计日志 / 导入日志 / 审计概览
├── sync_handler.go        离线批量上行
└── api_test.go            HTTP 层集成测试（httptest + 真实 PG）

scripts/smoke.sh           curl 串联全流程冒烟脚本
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **标准库 `net/http` + Go 1.22 `ServeMux`** | 不引 Gin/Echo；方法+路径参数原生支持，404 走 `GET /api/v1/{rest...}` 兜底成 JSON |
| **统一错误映射** | `store.ErrNotFound→404` / `ErrDuplicate→409` / `ErrInUse→409` / `ValidationError→400` / `ErrReasonRequired→400`，handler 只返回 `AppError` |
| **操作者中间件** | `X-Operator` 请求头写入 context，鉴权插槽落点；空操作者自动兜底为「系统」 |
| **未知字段拒绝** | 所有请求体用 `json.Decoder` 开启 `DisallowUnknownFields`，字段拼写不一致立刻 400 |
| **测试库物理隔离** | `service` 包用 `neuroscore_test`，`api` 包用 `neuroscore_test_api`。`go test ./...` 并行执行时互不 TRUNCATE，避免「单独跑能过、全量跑就挂」 |

### 验证记录（真实执行）

```
go build ./... / go vet ./... / gofmt -l   → 全部通过
bash scripts/test_db.sh                     → neuroscore_test + neuroscore_test_api 双库就绪
TEST_DATABASE_URL= postgres://... go test ./... → engine ok / service ok / api ok
engine 覆盖率                               → 100.0% of statements
service 覆盖率                              → 82.0%
api 覆盖率                                  → 78.4%（剩余多为错误分支与 httptest 不覆盖的 multipart 文件解析）
```

**HTTP 层 9 个集成用例（打到真实 PG）**

| 用例 | 验证内容 |
|---|---|
| `TestHealthz` | 健康检查返回 DB 连通状态 |
| `TestEventLifecycleOverHTTP` | 建赛项 / 列表 / 单查 / 校验 / 改配置 / 审计 / 快照 / 删除约束 |
| `TestTeamLifecycleOverHTTP` | 建队 / 一号一队 / 弃赛 / 恢复 / 改组 / 带成绩删队拒绝 |
| `TestImportOverHTTP` | 预览四色 / 行号勾选 / 冲突拦截 / 入库审计 |
| `TestScoreAndStandingsOverHTTP` | 录分 / 已签字锁定 / 改分申请 / 授权改分 / 按组别榜单 |
| `TestScheduleAndScreenOverHTTP` | 赛台 / 场次 / 自动分配 / 加时赛快照 / 大屏分页 / 锁定本场 |
| `TestAuditSummaryAndSyncOverHTTP` | 审计概览 / 离线批量上行 / 已签字成绩拒绝覆盖 |
| `TestBadRequests` | 未知字段 / 非法路径参数 / 非法时间 / 非法 JSON / 空操作人兜底 |

### 发现并修掉的一个问题

**测试库互相踩踏**：`go test ./...` 默认并行跑包，`service` 与 `api` 测试都 `TRUNCATE` 全表，共用一个库时症状是「单独跑能过、全量跑挂」。解决方案：
- `scripts/test_db.sh` 同时重建 `neuroscore_test` 与 `neuroscore_test_api`
- `internal/service/integration_test.go` 默认连接 `neuroscore_test`
- `internal/api/api_test.go` 默认连接 `neuroscore_test_api`

---

## P5 已完成：前端接入后端

### 交付文件

```
web/index.html              由 赛事统分后台管理_demo.html 迁移并增加后端适配层
web/embed.go                内嵌前端静态资源，生产模式单 exe 交付
赛事统分后台管理_demo.html  D:\Desktop\workbuddy\ 交付物副本（与 web/index.html 同步）
```

### 前端适配层能力

| 能力 | 说明 |
|---|---|
| **连接后端开关** | 顶部工具栏「连接后端」复选框，状态持久化到 localStorage |
| **健康检测** | 每 30 秒调用 `/healthz`；在线绿色、离线/异常黄色或红色 |
| **从后端加载** | 开启后自动拉取 `/events`、`/seats`、`/slots`、`/teams`、`/scores`、`/audit-logs`、`/import-logs`，转换为前端 S 并覆盖本地视图 |
| **成绩自动上行** | 本地 `save()` 触发 `backendSyncSoon()`，已签字成绩通过 `POST /sync` 批量推到后端 |
| **赛项配置反向同步** | 赛项与规则页新增「保存到后端」，调用 `PUT /events/:id` |
| **队伍反向同步** | 队伍管理页新增「推送队伍到后端」，按编号去重后 `POST /events/:eventId/teams` |
| **赛台赛程反向同步** | 赛台与赛程页新增「推送赛台赛程到后端」，按名称/赛台+时段去重后创建 |
| **lastSyncAt 持久化** | 上行成功后保存服务端返回的 `serverTime`，下次同步时传入 |
| **离线兜底** | 后端不可达时自动降级，数据始终先写 localStorage，断网不影响现场录分 |
| **手动刷新** | 开启后端模式后显示「刷新」按钮，强制从后端拉取全量 |

### 关键设计

- **前端 S 仍是单一事实来源**：后端模式是初始化/同步通道，视图渲染逻辑不变。
- **后端数据优先，本地永远兜底**：切换后端模式时若后端有数据则覆盖本地；关闭后端模式时回到本地数据。
- **成绩自动同步 + 资源手动推送**：成绩需要实时性，自动走 `/sync`；赛项/队伍/赛程批量推送由运营确认后触发，避免字段级修改产生大量请求。
- **同域部署零配置**：`BACKEND_URL='/api/v1'`，前端与 Go 服务同域；独立部署时可改此常量。

### 验证记录（真实执行）

```
服务启动：go run ./cmd/server（SCORE_DEV=true）/ 生产 exe（SCORE_DEV=false）
前端访问：curl http://127.0.0.1:8080/ → 返回 web/index.html（磁盘 / embed 两种方式均验证）
健康检查：curl /api/v1/healthz → {"database":"ok"}
赛项推送：PUT /events/front_test → 200，名称已更新
队伍推送：POST /events/front_test/teams → 201，按编号去重
赛程推送：POST /seats → 201；POST /slots → 201，按 seat+period 去重
成绩同步：POST /sync → 1 条成功 insert，服务端可查询
go test ./... / bash scripts/smoke.sh → 全绿
JS 语法：node -c 提取后的脚本 → 通过
```

### P5 后续可选增强

- [ ] **增量加载**：当前 `loadFromBackend` 是全量覆盖；后端 `/sync` 已记录 `lastSyncAt`，后续可基于它做增量合并。
- [ ] **本地 ID ↔ 后端 ID 映射**：当前队伍/赛台/场次推送后会创建新后端记录，反向改组/弃赛/改派需要维护映射表才能精确调用 PUT/DELETE。

---

## 已知问题 / 待办

### 🔴 需业务确认（引擎已能表达，等一个决策）

- [ ] **未打分的队伍也会拿奖**。奖项只按名次序号发放，因此「一场没比的队伍」照样能分到三等奖 —— 实测种子数据里未来之城的破晓队、智造队（总分 0、未完成录入）都拿到了奖项。
      引擎已提供 `RankOptions.AwardOnlyComplete`，置 true 后未完成录入的队伍**保留名次但不占获奖名额**。**正式公示前应开启**。
- [ ] **火星救援的加分规则影响力不低于主任务**。基准自检实测：加分项队间极差 **13.0** ≥ 基础分队间极差 **12.0**（`count_bonus` 的 `perUnit=5` 且 `cap=null`）。引擎算得没错，是**种子参数**的问题，需业务确认每块能量块/桥梁块的实际折算分与封顶。
- [ ] **奖项 `ceil` 的放大效应**：名额 = `ceil(队伍数 × 占比)`，队伍数少时会出现「全员获奖」（如 2 队的小组：一等奖 1 + 二等奖 1）。赛制若要求严格比例，需下调占比或改用 `floor`。

### 🟡 技术待办

- [ ] **改分申请单没有落库**。P3 的 `ScoreChangeRequest` 只写审计、不落申请记录，因此无法防止同一申请被重复提交，界面上也做不出「待审批队列」。需要一张 `score_change_requests` 表（0003 迁移）；届时**不需要改方法签名**，只需在 `RequestScoreChange` 里多写一次 INSERT
- [ ] **就近分配的排序依据是队伍编号**，不是叫号表。真实赛制应以 WRC 导出的叫号表为序，叫号表尚未接入（P6）。当前算法（按编号轮流铺到各赛台）是唯一确定且可解释的替代，运营可随时手动改派
- [ ] **`service` 覆盖率 82%**：剩余未覆盖的多为事务闭包内的 `return err` 分支，需故障注入才能触达。计分等核心计算在 engine 侧已是 100%
- [ ] **`-race` 竞态检测跑不了**：需要 cgo，而本机无 gcc。当前用「互斥锁 + 并发对称性测试」替代。若后续要上 CI，建议在带 gcc 的环境或 `CGO_ENABLED=1` 的容器里补跑一次
- [ ] `scripts/` 下缺 `pg_start.sh` / `pg_stop.sh`（当前靠手敲命令，P4 补上）
- [x] 前端静态资源已改用 `embed` 内嵌（`web/embed.go`），生产模式单 exe 交付
- [ ] 鉴权仅预留插槽（`api.CurrentUser`），本期不实现
- [ ] `x/text` 已从间接依赖提升为直接依赖（仅用于中文排序），二进制体积增加约 1MB。若在意可改注入 `CodepointNameLess` 换回码点序
- [ ] **PG 后端进程曾被 `0xC0000142`（DLL 初始化失败）杀掉一次** —— 典型是杀软拦截 fork。若后续频繁出现，需要把 `D:\Desktop\workbuddy\pgsql\bin` 加入杀软白名单
