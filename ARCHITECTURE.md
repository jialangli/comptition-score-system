# 赛事统分后台管理系统 · 后端架构设计

> 技术栈：Go（net/http + pgx/v5）+ PostgreSQL 17
> 定位：局域网部署、离线优先、单二进制交付
> 状态：P1 ~ P9 已完成（后端主体 + 前端接入 + 改分/争议/发布/锁与证据均已落地）；
> 其后为口径级变更，按日期记在 `PROGRESS.md` 与 `CHANGELOG.md`

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
├── migrations/                      # 手写顺序编号，up / down 成对（当前 0001 ~ 0014）
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
| `events` | 赛项：id / name / groups / **phases** / score_rule / bonus_rules / penalty_rule / rank_rule / custom_formula（规则部分存 **JSONB**，字段名与前端 Schema v1 完全一致）。`phases` = 阶段配置（迁移 `0018`）：多阶段赛项（未来之城 自动 120s + 手动 105s）的**时间奖励基准时长 = 各阶段之和**，与前端 `eventTotalSec` 同口径；它必须**落列**，否则配置经后端保存一次就被静默丢掉，且后端会按 120s 算时间奖励 |
| `tasks` | 任务项：event_id / id / name / type / max_score / weight / **unit** / control / enum_map。`unit` = **量词**（迁移 `0020`）：题卡上的「每颗 +100」里那个「颗」，只对 `type=count` 有意义；**可空，空 = 界面回落「每单位 N 分」**。刻意**不建 CHECK / 不枚举** —— 量词是文案，穷举必然漏（个/颗/块/堆/轮…），漏了就把现场配置挡在数据库外；合法性交 `engine.ValidateEvent`（非计数项填了量词 → 警告；超过 4 字或首尾有空白 → 拦下）。**它不参与任何算分**：`weight` 才是每单位分，量词只影响题卡文案 |
| `teams` | 队伍：**event_id / team_no（唯一）/ name / school / coach / group_code / status(active/withdrawn) / source(excel/manual/api) / seat_id(归台，NULL=未排台) / seat_order(台内顺位，从 1 起) / session(参赛轮次 1/2/both)** |
| `scores` | 打分记录：team_id / round_no / task_values(JSONB) / duration_sec / yellow（**黄牌计数，0..阈值-1**）/ upgraded_red（**已升级出的红牌数**）/ red / signed / operator / created_at |
| `seats` | 赛台：event_id / name / sort_order |
| `slots` | 场次：seat_id / period / time_range / event_id / group_code / **type(normal/extra/rematch)**（extra=加时赛、rematch=重赛，两者都是**独立场次**：不写队伍主库、成绩不自动进榜单）/ **round_no(1/2)**（迁移 `0017`；上午=第 1 轮、下午=第 2 轮，**落列而非现推**——1 轮赛项的下午场次仍是第 1 轮） |
| `slot_teams` | 场次-队伍 关联 —— **已退化为历史表**：场次队伍改为**派生**后不再读写它（迁移 `0017` 同批把 `POST /slots/{id}/teams` 改成 410）。保留数据以备回溯，但任何新代码都不应再写它 |
| `slot_snapshots` | **独立场次（加时赛 / 重赛）场内快照**：slot_id / team_no / name / school / coach —— 独立表，**不写主库** |
| `contest_rules` | **赛事级规则**（迁移 `0019`，一行一赛事）：contest_id / **substitute_mode(none/rank)** / substitute_note。`none` = 不递补（默认：取消资格队留下的名次位置**留空**，后续队伍保留原名次 → 公示表出现 4 → 6 的空洞）；`rank` = 按名次顺延。**作用域是赛事不是赛项** —— 与 `events` 里的赛项规则不是一个层级。**无记录 = 取代码里的默认值**（不给每个赛事预插一行）|
| `audit_logs` | 六类操作留痕：operator / action / target / before / after / reason / created_at |
| `import_logs` | 报名导入审计：source / operator / summary(JSONB) / detail(JSONB) / status |
| `config_snapshots` | 配置快照：note / events(JSONB) / created_at |
| `screen_config` | 大屏配置：event_id / page_size / interval_sec / pinned |

**关键约束**

- `teams(event_id, team_no)` 唯一索引 —— 落实「一号一队」强校验
- `audit_logs` 建 `created_at` 索引，保留 ≥2 年（迁移脚本里附归档说明）
- 所有 `*_id` 外键加 `ON DELETE RESTRICT`（避免误删带成绩的队伍）
- **唯一一道例外**：`teams.seat_id` → `seats(id)` 用 `ON DELETE SET NULL`（迁移 `0016`）。
  归台只决定「在哪张台打」，队伍本身不依赖赛台存在；用它 RESTRICT 会让
  「撤一张台」被「该台下还有队伍」拦下，逼运营先逐队取消归台 —— 而队伍并没有丢，
  它只是回到「未排台」，这本来就是删台的预期结果。存储层另在删台时把 `seat_order` 一并归零，
  读路径也会在 `seat_id` 为空时把顺位读成 0（防「未排台但顺位 3」的怪状态）。
- `teams.session` 与赛项轮次**取交集**才是该队真正要打的轮次（`TeamSession.Rounds`）——
  这是「按队伍参赛轮次判」的唯一判据；只按赛项轮次判会让「只打第 1 轮的队」永远算作第 2 轮未录。
  逐轮判据是 `TeamSession.Includes(round)`（场次派生用它，不需要知道赛项计划）。
  ⚠️ 交集为空时**回落赛项轮次**：否则配置矛盾（1 轮赛项 + 只打第 2 轮）会让该队**静默消失**，
  而不是留在待完成名单里被人发现。前端 `teamRounds` 与此逐字同口径。
- **场次队伍 = 派生，不是存储**（迁移 `0017` 起）：`场次 = 赛台 × 赛项 × 组别 × 轮次`，
  队伍由**队伍级归台**（`teams.seat_id` / `seat_order` / `session`）派生而来，
  顺序 = 台内顺位（现场叫号顺序）。唯一事实源只有一处，因此：
  - 改派 = `PUT /teams/{id}/seat`（写归台），**不是**写场次；
  - 写场次队伍的旧接口 `POST /slots/{id}/teams` 返回 **410**（保留路由给替代路径，而不是留一个 404 让人怀疑部署）；
  - `GET /slots/{id}/teams` 是**只读派生**读，也是平板端「本赛台队列」的权威数据源；
  - `slot_teams` 退化为历史表，任何新代码都不应再写它。
- **时间奖励的基准时长口径**（`engine.RefTimeFor`，迁移 `0018`）：优先级是
  **各阶段时长之和 → `scoreRule.params.refTime` → 默认 120s**，与前端 `eventTotalSec` 逐条对齐。
  🔴 阶段必须排第一：`params.refTime` 是「单阶段 / 无阶段」赛项用来填「赛项总时间」的地方，
  阶段才是更具体的事实源。顺序反了，在「既有阶段、又填了 refTime」的赛项上两端各算一套，
  而症状只是**名次对不上**（2026-10-10 实测未来之城前端 225s / 后端 120s，差出十几分）。
  判据集中在 `model.Event.PhaseTotalSec`，不要在别处再写一遍。
- **名次编排（递补规则）的判据只有一处**：`model.SubstituteMode.KeepGap()` → `engine.RankOptions.KeepGap`。
  ⚠️ 两端必须同口径，而且**只有存在被裁定取消资格的队伍时才看得出区别**：
  - 「不递补」（默认）作废队的位置留空 → 名次 4 → 6；「按名次顺延」编号连续。
  - **红牌取消比赛资格不产生空洞**（留在榜内、名次 0、排在最后），弃赛也不产生（压根不进原榜）。
  - 空缺按**组别**算（`RankAllGroups` 逐组发名次），一个组的空缺不会挪到另一个组。
  - 并列标记要与「**上一支有资格的队伍**」比，不能与上一行比 —— 上一行可能是作废队，
    那会把「并列」错判成「不并列」。这条已在 `TestRankKeepGap` 里用同分素材钉住。
  - 🔴 **奖项名额不跟着留空**（2026-10-10 定案）：作废行在 `assignAwards` **之前**整行剔除，
    于是它既不参与名额的分母（名额 = `floor(在榜队数 × 占比)`）也不当获奖人，让出的名额由
    后面队伍顶上 —— 10 队作废 1 队、三等奖 30%，名额由 3 个变 2 个。
    一句话：**名次上的空洞保留、奖项上的名额不留**（前者是身份标识，后者是名额分配）。
    归纳成一句：**只有「在榜且有资格」的队伍占名额** —— 作废队（已剔除，连分母都不参与）、
    红牌队（在榜、名次 0）、未完成录入队（在榜、有名次）都不占名额，名额一律顺延。
    守它的是 `TestRankVoidedTeamFreesAwardSlot`（10 支队素材 —— 2 队的素材配 floor + 保底
    会把两种口径算成同一结果，分不清）。
- **量词（`tasks.unit`）不参与算分**：它是文案，不是参数 —— 加字段时最容易顺手写进公式，所以有一条断言直接钉着「带量词与不带量词算出来的总分必须一模一样」（`TestTaskUnitRoundTrip`），另有一条断言 `engine/scoring.go` 里根本不出现 `.Unit`。
- `events.phases` 与 `groups` 同为 `JSONB NOT NULL DEFAULT '[]'`：**空态只有一种**。
  ⚠️ 写 JSONB 集合列一律走 `postgres.jsonArg`，它用**反射**把空/nil 集合归一成 `[]` / `{}`
  —— 因为 Go 的 nil slice 经 pgx 会被当作 SQL NULL，直接撞 NOT NULL；
  而白名单式写法每加一个列就会漏一个（`[]model.Phase` 就是这么漏的）。

---

## 5. API 设计（前缀 `/api/v1`）

| 资源 | 端点 |
|---|---|
| 赛项 | `GET/POST /events` · `GET/PUT/DELETE /events/{id}` · `POST /events/{id}/validate` |
| 配置快照 | `GET /events/{id}/snapshots` · `POST /events/{id}/snapshots` · `POST /events/{id}/snapshots/{sid}/restore` |
| 队伍 | `GET/POST /events/{id}/teams` · `PUT/DELETE /teams/{id}` · `POST /teams/{id}/withdraw` · `POST /teams/{id}/restore` · `PUT /teams/{id}/seat`（归台，seatId=0 取消） · `PUT /teams/{id}/session`（参赛轮次 1/2/both） |
| 报名导入 | `POST /imports/preview`（返回列映射 + 四色变更清单，不落库）· `POST /imports/commit` |
| 赛事级规则 | `GET/PUT /contest/rules`（**挂在顶层 /contest 下**：作用域是赛事；`{id}` 在别的路由里一律指赛项，混在一起会让人以为递补规则是逐赛项配的） |
| 赛台赛程 | `GET/POST /seats` · `GET/POST/PUT/DELETE /slots` · `POST /slots/{id}/auto-assign`（**一键自动分台**：写队伍级归台）· `GET /slots/{id}/teams`（**派生读**：本赛台队列的数据源）· `POST /slots/{id}/snapshot` · ⚠️ `POST /slots/{id}/teams` **已废弃 → 410**（改派走 `PUT /teams/{id}/seat`） |
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
11. **牌面口径只有一份，且读写两条路径都归一** —— 「几张黄牌升 1 张红牌」的阈值由赛项下发，
    黄牌**记满即转红并清零**，所以已升级出的红牌**必须单独落列**（`scores.upgraded_red`）。
    写路径 `NormalizeCards`（录入端与服务端共用）与读路径 `ResolveCards`（兼容仍是累计值的老数据）
    是同一套判据的两次表达 —— 与「计分双端逐位一致」同理，两侧各写一套必然漂移。

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
