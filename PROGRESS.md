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

## P6 已完成：改分申请单落库 + 奖项口径定调（2026-10-04）

### 交付文件

```
migrations/0003_score_change_requests.{up,down}.sql   申请单表 + 部分唯一索引
internal/store/postgres/change_request.go             仓储实现（6 个方法）
internal/store/repo.go                               ChangeRequestRepo 接口 + Repos.Changes
internal/model/score.go                              ScoreChangeRequest 补 ID/RoundNo/DecidedAt
internal/service/score_service.go                     落库 + 待审批队列 + 授权闭环
internal/service/service.go                          ErrChangePending / ErrAlreadyDecided
internal/engine/ranking.go                           奖项名额 ceil → floor（+ 每档保底 1）
internal/api/rank_handler.go                         awardComplete 默认开启
testdata/frontend_golden.json                        按新口径重算奖项
scripts/pg_start.sh / pg_stop.sh                     PG 幂等启停
_goenv.sh                                            构建入口（隔离 GOROOT 与沙箱代理）
```

### 关键设计

- **防重复放数据库，不放应用层**：对 `(team_id, round_no)` 建「仅未审批」的部分唯一索引
  `ux_scr_one_pending`。若改成「应用层先查再插」，并发下两个请求都会查到「没有」然后都插进去。
  冲突经 `mapError(23505)` → `store.ErrDuplicate` → 翻译成 `ErrChangePending`。
  索引带 `WHERE NOT approved`：批完之后若确需再改，仍可再次发起。
- **`ApplyScoreChange` 签名未变**，只在事务内多一步：若该队该轮有挂着的申请单，一并标记为已授权。
  查不到申请单不算错（裁判长可对紧急情况直接改分，仍留痕）—— 申请单只是「裁判发起」那条路径的产物。
- **奖项改 floor，并显式处理「名额为 0」**：floor 管住了「不超编」，但 3 队 × 0.1 = 0.3 → 0 个名额，
  会出现「一等奖 3 人、二等奖 0 人、三等奖 0 人」。因此对每个 `ratio > 0` 的档位**保底 1 个**，管住「不空档」。

### 验证记录（真实执行）

```
PostgreSQL 启动：pg_ctl status → server is running (PID 3080)，isready → accepting connections
迁移执行：test_db.sh → score_change_requests 建表 + 3 个索引（含 ux_scr_one_pending），两个测试库均已建好
go build -buildvcs=false ./...  → 通过
go vet                              → 通过（0 告警）
go test -p 1 ./...（串行）          → 全部 ok（api / engine / service 三包）
go test ./...（默认并行）           → service 包 8 个 FAIL：events_pkey 编号重复
```

**关于并行失败**：service 包内多个测试共用同一个库，各自创建 `brain_planet` 赛项，
`go test` 默认并行执行 → 后一个撞主键。`-p 1 -parallel 1` 串行后全绿。
**这是测试隔离问题，不是代码缺陷**（与 P3 记录的「两包共用一库会互相踩踏」同类）。
后续修法：给 service 包内每个测试分配独立 schema，或在 `newSvc(t)` 里用唯一赛项 id。

### 已知限制

- `go build` 需带 `-buildvcs=false`：本机 `.git/refs` 缺失（见下方「环境注意事项」），VCS 标记失败。
- 沙箱 Bash 注入 `HTTP_PROXY=127.0.0.1:18211`，`go mod download` 会被拒；用 `_goenv.sh` unset 掉。

---

## P7 已完成：争议工单落库（2026-10-05）

### 背景

前端 P8 家族定义了一整套争议裁定流程，但后端**没有承载它的实体**。此前只有
`score_change_requests`（改分申请单），二者是两回事：改分申请单是「我要把这个分改成 X」
（申请改数），争议工单是「出现两份成绩 / 申诉，请判定是非」（裁定结论）。
缺口导致 P8 队列无处查询、裁定三态没有落点、E 项「补传冲突自动建单」无处可建。

### 交付文件

```
migrations/0005_disputes.{up,down}.sql   争议工单表 + 部分唯一索引
internal/model/dispute.go                工单模型 + 三档裁定结论 + 中文标签
internal/model/audit.go                  新增「上报争议 / 裁定争议 / 撤回争议」三个动作
internal/store/repo.go                   DisputeRepo 接口 + Repos.Disputes
internal/store/postgres/dispute.go       仓储实现（6 个方法）
internal/store/postgres/admin.go         TruncateAll 补 disputes
internal/service/dispute_service.go      上报 / 系统建单 / 撤回 / 裁定 / 队列查询
internal/service/service.go              ErrDisputePending / ErrDisputeNotPending
internal/api/dispute_handler.go          6 个端点 + 参数校验
internal/api/router.go / response.go     路由注册 + 语义错误映射
internal/api/sync_handler.go             /sync 补传冲突自动建单（E 项联动）
internal/service/dispute_test.go         6 个集成用例（真实 PG）
internal/api/dispute_test.go             1 个 HTTP 集成用例
internal/api/sync_conflict_test.go       1 个补传冲突建单用例
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **状态三态而非布尔** | 待裁定 / 已裁定 / 已撤回。撤回是独立状态而非物理删除 —— 工单提错了也要留痕，与「队伍弃赛只做软删除」同一条原则 |
| **去重靠部分唯一索引** | `ux_disputes_one_open ... WHERE status='pending'`。离线补传是并发的，「先查再插」两个请求都会查到「没有」然后都插进去。带 WHERE 是为了已裁定 / 已撤回之后还能再提 |
| **允许再裁定** | `Decide` 刻意不加 `AND status='pending'` 守卫（与 `ChangeStore.Decide` 相反）。P8e 明确支持「确需推翻时再裁定一次并留痕」，每次裁定都写审计，本表只保留最新结论 |
| **系统建单幂等** | 补传会重试，同一队同一轮重复撞车只建一条工单；并发下被唯一索引挡下的后来者同样按幂等处理，不打断 `/sync` 主流程 |
| **来源列区分人工与系统** | `source = referee / system`，共用同一队列与裁定流程，但「提出人 / 来源」列必须能分开，否则运营分不清是人报的还是补传撞车 |
| **同步冲突不接受人工上报** | 它不是人能观察到的现象，让人去提只会产生来源与事实不符的工单。HTTP 层直接 400 |
| **结论只记录、不联动改榜** | `verdict=disqualify` 后的名次顺延与递补由工作人员在后台执行（P8c note 7 口径），避免「裁定」与「执行」耦合 |
| **`TruncateAll` 同步补表** | 新增表必须一并更新清单，否则测试间互相污染，症状是「单独跑能过、全量跑就挂」 |

### 与前端 P8 家族的对应

| 前端页 | 后端能力 |
|---|---|
| P8 待裁定队列 | `GET /api/v1/disputes`（OpenDisputes） |
| P8a 维持原判 / P8b 授权改分 / P8c 取消资格 | `POST /api/v1/disputes/{id}/decide`，verdict = uphold / adjust / disqualify |
| P8e 已裁定只读态 + 再裁定一次 | 同一 decide 端点可重复执行，每次留痕 |
| P2「我的申请」→ 撤回 | `POST /api/v1/disputes/{id}/withdraw` |
| P2e2 已作废成绩单 | `GET /api/v1/teams/{id}/disputes` 判断该队是否被判取消资格 |
| P12 离线补传同步冲突 | `service.ReportSyncConflict`（**已就绪，尚未接到 /sync**） |

### 验证记录（真实执行）

```
bash scripts/test_db.sh   → neuroscore_test / neuroscore_test_api 均 15 张表（新增 disputes）
go build ./...            → BUILD_OK
go vet ./...              → 0 告警
gofmt -l（新增文件）       → 全部规范
go test -p 1 -count=1 ./... → api ok (5.6s) / engine ok (1.3s) / service ok (9.0s)
```

**新增 7 个集成用例（全部打到真实 PG，无 mock）**

| 用例 | 验证内容 |
|---|---|
| `TestDisputeLifecycle` | 上报 → 进队列 → 裁定（取消资格）→ 出队列且结论落库 → 历史仍可查 → 上报与裁定各留 1 条审计 |
| `TestDisputeDuplicateReportBlocked` | 同队同轮同类型重复上报被唯一索引挡下；换类型 / 换轮次仍可再提 |
| `TestDisputeSyncConflictIdempotent` | 补传重试只建一条工单；来源为 system、类型为 sync_conflict |
| `TestDisputeWithdraw` | 撤回出队列但留记录；重复撤回与裁定已撤回工单均被拒 |
| `TestDisputeRedecide` | 已裁定可再裁定，结论覆盖最新、审计留 2 条 |
| `TestDisputeReasonRequired` | 上报与裁定都必须说明原因 |
| `TestDisputesOverHTTP` | 6 个端点 + 参数校验：非法类型/轮次/结论 400、同步冲突人工上报 400、重复上报 409、已裁定不可撤 409 |

### 后续（P0 剩余，按依赖顺序）

- [x] **E 项联动**：`/sync` 补传冲突自动建单 —— **已接线**，见下节。
- [ ] **P0-1 发布 / 移交 / 回流状态机**：前端 P11 + P13 已上线四态看板，后端仍**完全空白**（0 处匹配）。
- [ ] **P1 裁判码**（6 位 · 首登激活 · 断网可登）与**赛台-队伍可写锁**：后端均无。
- [ ] **P1 留底证据库**：无专门表（`audit_logs` 部分覆盖）。

### E 项联动：/sync 补传冲突自动建单

此前 `sync_handler.go` 在「服务端已存在同队同轮记录」时**静默取最新覆盖** ——
正是前端 P12 note 7 点名要消灭的「两份成绩静静躺着、取数默认取最新而错榜」。

**处置改为「不覆盖 + 自动建单」**，两个分支都接上：

| 场景 | 处置 |
|---|---|
| 服务端已有记录、来自**另一来源** | 不写入；自动生成同步冲突工单；结果标记 `conflict` 并带回工单号 |
| 服务端记录**已签字**、来自另一来源 | 同样建单（只回「去走改分申请」会让后上传的那份无声消失） |
| **同源**（同一 clientId 续传 / 断线重传） | 照旧放行覆盖 —— 否则每次网络重试都会凭空建一张工单 |

判据用 `operator` 而不是成绩内容：要防的是「两份成绩并存」这个事实，
不是「两边打得不一样」。同源判定要求两端 operator 都非空且相等 ——
服务端那条若没有 operator（例如后台人工录的），宁可判成冲突也不覆盖。

响应新增 `conflicted` 计数与单条的 `conflict / disputeId / disputeCode`。
`conflicted` 单列而不并入 `failed`：冲突不是「没传上去」，而是「已转人工裁定」，
两者处置完全不同（前者要重传，后者要等裁定）。前端目前只消费 `succeeded` 与
`serverTime`，新增字段不影响既有逻辑。

验证：`TestSyncConflictAutoDispute` 覆盖首次入库 / 同源续传不误判 /
异源冲突不覆盖 / 工单进 P8 队列且来源类型可分辨 / 第三次撞车幂等。

---

## P8 已完成：发布状态机 + 裁判码（2026-10-05）

### 背景

两块都是「前端已上线、后端完全空白」的脱节点：

- **发布 / 移交 / 回流**：P11「确认并移交」+ P13「发布状态回流看板」定义了完整链路，
  后端此前 0 处匹配 —— 裁判长点了移交之后无从知道后台到底发出去没有。
- **裁判码**：P1 家族的登录链路完全建立在它上面，后端零支持
  （测试里的「裁判A」只是 operator 字符串）。

### 交付文件

```
migrations/0006_release_units.{up,down}.sql    发布单元 + 四态状态机
migrations/0007_referee_codes.{up,down}.sql    裁判码（赛事级凭证）
internal/model/release.go                      四态 + 前端两列文案派生
internal/model/referee.go                      裁判码档案 + 身份 / 状态
internal/model/audit.go                        新增 7 个审计动作
internal/store/repo.go                         ReleaseUnitRepo / RefereeCodeRepo
internal/store/postgres/{release,referee}.go   仓储实现
internal/store/postgres/admin.go               TruncateAll 补两张表
internal/service/release_service.go            移交 / 接收 / 发布 / 标记重发
internal/service/referee_service.go            建档发码 / 激活（crypto/rand）
internal/api/{release,referee}_handler.go      10 个端点
internal/api/router.go / response.go           路由 + 三种失败的状态码区分
internal/service/{release,referee}_test.go     7 个集成用例
internal/api/{release,referee}_test.go         2 个 HTTP 集成用例
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **发布单元 = 赛项 × 组别 × 赛台** | 来自 P13 定义。一个赛项在三个赛台上是三个单元，分别移交、分别发布 |
| **只存一个 status，两列文案派生** | 前端「移交状态」与「发布状态」两列是同一状态的两种视图；存两份真值迟早打架 |
| **重发用标记，不新增第五个状态** | 已发布后发生改分 / 裁定 → 回退 pending + `republish_required`。它与首次待发布在后端是同一件事（都是等运营点发布），差别只在前端标不标红 |
| **状态机由数据库把关** | 每条 UPDATE 带允许的前置状态（如 `status IN ('handed','pending')`），命中 0 行即视为状态不符。并发下两个运营同时点发布只有一个会成功，不会重复发布也不会静默覆盖 |
| **重发保留上次发布人** | 重发期间前端既要提示「待重发」，也要能看到上一版是谁发的 |
| **裁判码三种失败必须可区分** | 码无效 404（P1.5b）/ 姓名不匹配 400（P1.5c）/ 已作废 409。统一成「登录失败」前端就没法分流，而这两种失败给裁判的补救动作完全不同 |
| **激活幂等** | 换平板、重装 App 都要能重新激活；已激活的码再激活直接返回绑定、不重复留痕 |
| **裁判码是赛事级凭证** | contest_id 分区 + `UNIQUE(contest_id, code)`。旧码在新赛事里就是查无此码 |
| **码用 crypto/rand + 无歧义字符集** | 剔除 0/O、1/I/L —— 裁判码是口头传达 + 手输的凭证，留易混字符等于凭空制造登录失败；用 crypto/rand 而非 math/rand 是因为它是凭据，可预测就等于没有 |

### 与前端的对应

| 前端页 | 后端能力 |
|---|---|
| P11 确认并移交 | `POST /api/v1/releases/hand-over`（按赛项 / 组别 / 赛台定位） |
| P13 发布状态回流看板 | `GET /api/v1/releases`（含四态计数与待重发数） |
| P13 note 5 重发联动 | `POST /api/v1/releases/{id}/republish` |
| P1 登录 / P1.5 · P1.6 绑定确认 | `POST /api/v1/referee-codes/activate`（回传预绑执裁范围） |
| P1.5b 码无效 / P1.5c 姓名不匹配 | 由 activate 的 404 / 400 区分 |

### 验证记录（真实执行）

```
bash scripts/test_db.sh   → 两库均 17 张表（新增 release_units / referee_codes）
go build ./...            → BUILD_OK
go vet ./...              → 0 告警
gofmt -l（新增文件）       → 全部规范
go test -p 1 -count=1 ./... → api ok (7.5s) / engine ok (1.6s) / service ok (13.1s)
```

**新增 9 个集成用例（全部打到真实 PG）**

| 用例 | 验证内容 |
|---|---|
| `TestReleaseLifecycle` | 四态主链路 + 发布人 / 时间落库 + 每步留痕 |
| `TestReleasePublishRequiresHandover` | 未移交不得发布；移交后即可发布（不必等接收） |
| `TestReleaseRepublish` | 回退到待发布 + 重发标记 + 保留上次发布人 + 重发后标记清空 |
| `TestReleaseEnsureIdempotent` | 同一三元组 Ensure 两次是同一单元；换组别是另一单元 |
| `TestRefereeIssueAndActivate` | 6 位码、预绑范围、激活、重复激活幂等且不重复留痕 |
| `TestRefereeActivateFailures` | 码无效 / 姓名不匹配 / 空码三种失败可区分 |
| `TestRefereeCodeIsContestScoped` | 跨赛事使用旧码无效（赛事级凭证不变式） |
| `TestReleasesOverHTTP` | 7 个端点 + 409 未移交发布 + 参数校验 + 看板计数 |
| `TestRefereeCodesOverHTTP` | 建档 / 激活 / 三种失败状态码 / 重复激活幂等 |

### 后续

- [x] **P1 赛台-队伍可写锁** —— 已在 P9 完成（0008）。
- [x] **P1 留底证据库** —— 已在 P9 完成（0009）。
- [ ] **本期不做**：裁判码不签发会话（鉴权仍是 `api.CurrentUser` 插槽）；
      离线登录由平板端凭本机缓存判定，不回后端

---

## P9 已完成：赛台-队伍可写锁 + 留底证据库（2026-10-06）

### 背景

- **可写锁**：E 项此前只做了「冲突**发生后**自动建单」，缺**预防**机制。
  P12 note 7 要的是从源头掐断「两台平板各打一份」。
- **留底证据库**：前端多处引用「证据层 / 留底三件」，后端无专门表。

### 交付文件

```
migrations/0008_team_write_locks.{up,down}.sql  赛台-队伍可写锁
migrations/0009_evidence.{up,down}.sql          留底证据库
internal/model/{lock,evidence}.go               模型 + 中文标签
internal/store/repo.go                          WriteLockRepo / EvidenceRepo
internal/store/postgres/{lock,evidence}.go      仓储实现
internal/store/postgres/admin.go                TruncateAll 补两张表
internal/service/{lock,evidence}_service.go     抢锁 / 查询 / 释放 / 证据登记与上云
internal/api/{lock,evidence}_handler.go         9 个端点
internal/service/{lock,evidence}_test.go        7 个集成用例
internal/api/{lock,evidence}_test.go            2 个 HTTP 集成用例
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **锁的粒度是（赛台, 队伍），不含轮次** | 同一台平板要连续打完第 1、2 轮；按轮次划分会在两轮之间留下被抢走的窗口 |
| **唯一索引即锁** | `ux_team_write_lock`。抢占用 `INSERT ... ON CONFLICT ... WHERE`，由数据库裁决谁是第一个。应用层「先查再插」在两台平板同时提交时两边都会查到「没有」 |
| **一条 SQL 覆盖四种情形** | 无人持锁→插入；锁已过期→覆盖；自己持锁→续期；他人持锁→WHERE 不成立、返回 0 行→拿不到 |
| **锁必须有 TTL** | 没有 TTL 的锁会把一支队伍**永久锁死**（平板没电 / 掉线），比不加锁更糟。默认 2 小时 |
| **只有持锁者能释放** | 释放带 `AND holder = $n`。否则 A 正在打分，B 点一下就把 A 的锁解了 |
| **抢不到锁不是错误** | 返回 200 + `writable=false` 与持锁者信息。另一台平板该显示「该队正由 X 执裁」，而不是拿到 4xx 后反复重试 |
| **锁不写审计** | 抢锁发生在每一次提交 / 暂存，频率极高，写审计会冲垮 `audit_logs`、稀释真正需要追溯的操作 |
| **证据只存元数据** | 不收二进制本体 —— 图片本体属于对象存储的职责。后端回答的是「三件齐不齐、谁产生的、上云了没有」 |
| **产生端必须可区分** | `source` = 裁判提交成绩 / 裁判长提交裁定 / 工作人员发布（P2e note 7「规范统一、触发点各异」） |
| **补传重试不去重会让三件变七件** | 唯一索引落在（队伍, 轮次, 类型, 文件名） |

### 验证记录（真实执行）

```
bash scripts/test_db.sh   → 两库均 19 张表（新增 team_write_locks / evidence）
go build ./...            → BUILD_OK
go vet ./...              → 0 告警
gofmt -l（新增文件）       → 全部规范
go test -p 1 -count=1 ./... → api ok (8.7s) / engine ok (1.4s) / service ok (18.0s)
```

**新增 9 个集成用例（全部打到真实 PG）**

| 用例 | 验证内容 |
|---|---|
| `TestWriteLockExclusive` | A 抢到 → B 抢不到且带回持锁者与提示 → A 释放后 B 抢到 |
| `TestWriteLockSelfRenewAndIsolation` | 自己续期不掉锁；别人解不开；裁判长可强制释放 |
| `TestWriteLockExpiry` | 过期锁可被直接抢占（没有 TTL 会把队伍永久锁死） |
| `TestWriteLockDifferentSeats` | 不同赛台是不同锁，互不影响 |
| `TestEvidenceThreePieces` | 三件齐全性判定 + 待上云队列 + 补传上云流转 |
| `TestEvidenceDuplicateBlocked` | 补传重试不产生重复证据 |
| `TestEvidenceSourceDistinct` | 产生端可区分，且裁定类证据可无轮次 |
| `TestLocksOverHTTP` | 抢不到锁返回 200 而非 4xx；别人释放 404；参数校验 |
| `TestEvidenceOverHTTP` | 三件齐全性接口 + 待上云 + 上云 + 重复 409 + 非法 kind/source 400 |

### 已知边界

- **证据本体不入库**：`storage_url` 留给后续接入对象存储时填写，当前为空。
- **锁只是并发互斥**，不承载业务结论；两台平板仍可能因离线各自打分 ——
  那条路径由 E 项的补传冲突自动建单兜底。
- **本期不做**：锁与 `/sync` 的联动（提交时自动抢锁）。当前锁由平板端显式调用，
  后端已具备能力，接与不接取决于平板端实现节奏。

---

## 判罚升级口径落地：黄牌记满即清零（2026-10-10）

### 背景

需求原文：「裁判端的打分页需要与后台管理的赛项与规则配置 · 判罚规则的累计升级阈值进行链接，
选的是超过三张黄牌转化为一张红牌，那么这个裁判的打分页的黄牌计数器最大只能为 3」。

核下来这不是给输入框加个 `max` 属性 —— 它**改了黄牌的数据语义**：

| | 旧口径 | 新口径 |
|---|---|---|
| `yellow` 含义 | 不封顶的累计值（可能 4、5、6） | **当前周期计数**（`0 .. 阈值-1`） |
| 红牌数 | `直接记的 + floor(yellow / 阈值)`，现算 | `直接记的 + upgraded_red` |
| 计数器上限 | 无 | **= 阈值**（由后台按赛项下发） |

**关键推论（决定了本次必须加列）**：清零之后 `floor(yellow / 阈值)` 恒为 0 ——
已经升级出来的红牌**算不出来了**，必须落到独立字段。
所以 `scores.upgraded_red` 不是设计选择，是「归零」方案的数学后果。

### 交付文件

```
migrations/0014_score_card_yellow_clear.{up,down}.sql   加列 + 历史值折算
internal/engine/cards.go                                新增 NormalizeCards（写路径归一）
                                                        ResolveCards 改为「当前周期黄牌 + 已升级红牌」
internal/model/score.go                                 ScoreRecord 补 UpgradedRed
internal/api/{params,score_handler,sync_handler}.go     三个请求 DTO 补字段
internal/service/score_service.go                       SaveScore 落库前先归一
internal/store/postgres/score.go                        SELECT / Scan / UPSERT 带新列
internal/engine/cards_test.go                           表驱动用例补「期望黄牌计数」一列
internal/service/integration_test.go                    TestScoreCardYellowClearsAtThreshold
web/index.html                                          离线补传请求体补 upgradedRed（见下）
index.html · 赛事统分后台管理_demo.html · 裁判打分系统_wireframe.html   前端三处同口径
```

### 关键设计

| 决策 | 说明 |
|---|---|
| **读写两条路径，同一套口径** | `NormalizeCards`（写：把「要记的黄牌总数」折算成 `(黄牌计数, 升级红牌数)`）与 `ResolveCards`（读：兼容仍是累计值的老数据）表达的是一条规则。**录入端与服务端共用** —— 各算一套必然漂移 |
| **关掉开关 = 规则真的不生效** | `cardRules.enabled=false` 时黄牌不累计、也不升级红牌，只剩裁判直接记的红牌。不是「把界面藏起来」 |
| **升级那一刻必须显式提示** | 红牌 = 当场取消比赛资格（重后果），而升级是系统自动触发的。若静默发生，操作者不会知道自己刚记下的第 3 张黄牌变成了红牌 |
| **`/sync` 必须带上新字段** | 前端归零后是 `yellow=0, upgradedRed=1`；只发 `yellow`/`red` 时服务端会算出**红牌 0 → 取消资格静默失效**（成绩看着对，红牌没了）。补传正是离线场景的主路径 |
| **迁移不可逆要写明** | 归一后原「累计黄牌数」不再单独保留（追溯看操作审计里的逐次记牌记录），`down` 只回滚列 |
| **阈值逐赛项取** | `events.penalty_rule -> 'cardRules' ->> 'redThreshold'`，缺省 3；关闭黄牌计数器的赛项不动 |

### 验证记录（真实执行）

```
bash scripts/test_db.sh                → 迁移 0014 应用成功
go build ./... / go vet ./...          → 通过、0 告警
gofmt -l internal/ cmd/                → 无输出
go test -p 1 -count=1 ./...            → api ok (18.5s) / engine ok (1.2s)
                                          model ok (1.2s) / service ok (17.5s)   ← 真连 PG
前端全量（30 个脚本，含 4 个探针）      → 1501 项 0 失败
```

**新增集成用例 `TestScoreCardYellowClearsAtThreshold`**（打到真实 PG）一次证明三件事：

1. **新列真贯通** —— 写 `upgraded_red=1 / yellow=0`，读回仍是 `1 / 0`；
2. **写路径归一真生效** —— 请求里发「要记 5 张黄牌」（阈值 3），落库是 `yellow=2, upgraded_red=1`，
   即服务端不会把 5 张原样存下来；
3. **重复归一不叠加** —— 同一条记录再归一一次结果不变（幂等），否则补传重试会把红牌越滚越多。

### 已知边界

- **归一不可逆**：历史 `yellow` 已被折算，`down` 只能回滚列、回不到原累计值。
- **前端 demo 的分数仍存在浏览器 localStorage**（`S.scores[teamId] = {1: rec, 2: rec}`），
  与后端两轮结构同构但**不是同一份数据** —— 该前端是原型，不接后端。

---

## 已知问题 / 待办

### ✅ 已定调（2026-10-04）

- [x] **未打分的队伍也会拿奖** → API 层 `awardComplete` 默认改为 **true**。
      未完成录入的队伍**保留名次但不占获奖名额**；需要看旧口径时显式传 `?awardComplete=0`。
      基准已按新口径重算（未来之城破晓队、智造队不再获奖）。
- [x] **奖项 `ceil` 的放大效应** → 改为 `floor` + 每档保底 1 个名额，并补
      `TestRankAwardsUseFloorNotCeil` 锁定语义（3 队 × 0.34 只发 1 个，ceil 会发 2 个）。
- [x] **改分申请单没有落库** → 0003 迁移已补，含待审批队列接口与防重复约束。

### 🔴 需业务确认

- [ ] **火星救援的加分规则影响力不低于主任务**。基准自检实测：加分项队间极差 **13.0** ≥ 基础分队间极差 **12.0**（`count_bonus` 的 `perUnit=5` 且 `cap=null`）。引擎算得没错，是**种子参数**的问题，需业务确认每块能量块/桥梁块的实际折算分与封顶。

### 🟡 技术待办

- [ ] **service 包内测试共用一个库**：`go test` 并行执行时多个测试抢建同名赛项 → `events_pkey` 冲突。
      串行（`-p 1`）可过。修法：每测试独立 schema，或 `newSvc(t)` 生成唯一赛项 id。
- [ ] **就近分配的排序依据是队伍编号**，不是叫号表。真实赛制应以 WRC 导出的叫号表为序，叫号表尚未接入（P6）。
      当前算法（按编号轮流铺到各赛台）是唯一确定且可解释的替代，运营可随时手动改派
- [ ] **队伍级归台（`teams.seat_id` / `sort_order`）后端不存在** —— 前端原型已把它定为
      「队伍落在哪张台」的**唯一事实源**（场次队伍改为派生，见 CHANGELOG 2026-10-09 那批），
      但库表里没有这两列、后端也**没有队列接口**。因此平板端「本赛台队列」目前**没有权威数据源**。
      补齐 = 迁移（加列或 `team_seats` 表）+ 按 `slot_teams` 反推回填 + 与既有
      `slots/{id}/auto-assign`、`slots/{id}/teams` 两个接口重新对齐（它们与「派生」语义冲突）
- [ ] **`service` 覆盖率 82%**：剩余未覆盖的多为事务闭包内的 `return err` 分支，需故障注入才能触达。计分等核心计算在 engine 侧已是 100%
- [ ] **`-race` 竞态检测跑不了**：需要 cgo，而本机无 gcc。当前用「互斥锁 + 并发对称性测试」替代。若后续要上 CI，建议在带 gcc 的环境或 `CGO_ENABLED=1` 的容器里补跑一次
- [x] `scripts/pg_start.sh` / `pg_stop.sh` 已补（幂等 + postmaster.pid 残留自愈 + 失败打印日志尾部）
- [ ] **鉴权仅预留插槽**（`api.CurrentUser`），本期不实现
- [ ] **PG 需杀软白名单**：`D:\Desktop\workbuddy\pgsql\bin` 与 `D:\Desktop\workbuddy\pgdata`，
      否则 `pg_control: Permission denied`（2026-10-04 已加白名单后恢复正常）

- [x] 前端静态资源已改用 `embed` 内嵌（`web/embed.go`），生产模式单 exe 交付
- [ ] 鉴权仅预留插槽（`api.CurrentUser`），本期不实现
- [ ] `x/text` 已从间接依赖提升为直接依赖（仅用于中文排序），二进制体积增加约 1MB。若在意可改注入 `CodepointNameLess` 换回码点序
- [ ] **PG 后端进程曾被 `0xC0000142`（DLL 初始化失败）杀掉一次** —— 典型是杀软拦截 fork。若后续频繁出现，需要把 `D:\Desktop\workbuddy\pgsql\bin` 加入杀软白名单
