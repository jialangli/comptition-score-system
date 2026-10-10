# Contributing to NeuroMaster 赛事评分系统

感谢你愿意参与改进。这个仓库同时包含**产品原型（单文件 HTML）**与**Go 后端**，两类贡献的流程不同，请先确认你要改的是哪一部分。

## 贡献前必读

| 你要改什么 | 必读 |
|---|---|
| 后台 demo / 裁判端 wireframe | 本文「原型改动约定」+ [ARCHITECTURE.md](ARCHITECTURE.md) |
| Go 后端 | 本文「后端开发」+ [ARCHITECTURE.md](ARCHITECTURE.md) 的分层铁律 |
| 文档 | 直接提 PR |

---

## 一、通用约定

### 分支与提交

- 从 `main` 切特性分支：`feat/<简述>`、`fix/<简述>`、`docs/<简述>`
- 提交信息首行用中文说明「做了什么」，正文写「为什么」。示例：

```
feat(排名): 奖项名额改用 floor 并对每档保底 1 个

3 队 × 0.1 = 0.3 → floor 得 0 个名额，会出现「一等奖 3 人、
二等奖 0 人」。floor 管住不超编，保底管住不空档。
```

### 演示数据

仓库内所有队伍、学校、成绩、人名均为**虚构**，仅存于浏览器 localStorage。
**不要**把任何真实赛事数据、真实学生姓名或联系方式提交进来。

---

## 二、原型改动约定（单文件 HTML）

两个原型都是**单文件、零依赖、内联全部 CSS 与 JS** 的交付物。这不是偷懒，是刻意的产品决策：现场裁判用平板双击就能打开，不需要构建、不需要联网。

因此改动必须遵守：

### 1. 行尾一律 CRLF

Windows 交付物统一 CRLF。用脚本改文件时：

```python
# 读：归一为 \n 便于处理
s = io.open(path, "r", encoding="utf-8", newline=None).read()
# 写：禁止 Python 二次转换，手动转回
io.open(path, "w", encoding="utf-8", newline="").write(s.replace("\n", "\r\n"))
```

⚠️ 若用 `newline=None` 读入后直接写回、忘了手动转换，**整个文件会被静默改成 LF**。

### 2. 每处替换带 count 断言

不要在编辑器里手动改大文件。写 Python 脚本批量替换，命中数不符就中止：

```python
def do(old, new, n):
    c = s.count(old)
    assert c == n, "count %d (want %d): %r" % (c, n, old[:70])
    s = s.replace(old, new, n)   # 用 n 而非 0：多改了就报错
```

### 3. 在单引号字符串里嵌单引号

生成 `onclick="fn('id',v)"` 这类 HTML 时，**不要**用 `\'` 转义（会让 id 变字面量或提前闭合字符串）。取字符再拼：

```js
var Q = String.fromCharCode(39);   /* 单引号 —— 避免在单引号字符串里再嵌单引号 */
'...onclick="fn(' + Q + id + Q + ',this.checked)">'
```

### 4. 改完必跑三道校验

```bash
# 1) 语法：node --check 不接受 .html，先抽 <script>
node -e "const fs=require('fs');const m=fs.readFileSync('f.html','utf-8').match(/<script>([\s\S]*)<\/script>/);fs.writeFileSync('_x.js',m[1]);"
node --check _x.js

# 2) 逻辑：跑回归入口（见下节）
node _全部回归_2026xxxx.js

# 3) 结构配平：<div> 与 </div> 数量必须相等
```

### 5. 前端计分必须与 Go 引擎逐位一致

计分、排名、脱敏是**双实现**（浏览器 JS + Go 引擎）。
改任何一侧，另一侧与 `testdata/frontend_golden.json` 都要同步更新，并确认 `TestParityWithFrontend*` 通过。
**不要**只改一边 —— 那会让公示表与榜单对不上。

基准的重新生成（改了前端算法后必须跑）：

```bash
node scripts/gen_frontend_golden.js "D:/Desktop/workbuddy/赛事统分后台管理_demo.html"
bash _goenv.sh test ./internal/engine ./internal/service -count=1 -p 1
```

两点要记住：

1. **脚本会自己报错**，不要绕过它：入口函数被改名 → 抽取阶段就崩；
   基准少了某类分支（两轮 / 弃赛 / 取消资格 / 并列 / 记分与扣分为 0 …）→ 覆盖度自检退出非 0。
   它崩了**比它悄悄产出旧内容好**：这份基准曾因脚本静默失效而脱离前端半年。
2. **`excluded` 要读**：基准里显式登记了「没有对照什么、为什么」（递补口径、红牌跨轮语义、
   脱敏输出、奖项档位…）。加断言前先看一眼 —— 否则会把已知分歧当成新 bug，
   或者反过来把"没测"当成"已对齐"。

---

## 三、后端开发

### 环境

```bash
source scripts/env.sh        # 或 ./_goenv.sh，两者都可用
go version                   # 需要 go1.25.0
```

### 启动依赖

```bash
bash scripts/pg_start.sh     # 幂等；PG 未起会提示杀软白名单问题
bash scripts/test_db.sh      # 建两个测试库并执行 0001~0003 迁移
```

> PostgreSQL 需在杀软白名单内（`pgsql\bin` 与 `pgdata`），否则报
> `could not open file "global/pg_control": Permission denied`。

### 跑测试

```bash
source _goenv.sh
export TEST_DATABASE_URL="postgres://postgres@127.0.0.1:5432/neuroscore_test?sslmode=disable"
export TEST_DATABASE_URL_API="postgres://postgres@127.0.0.1:5432/neuroscore_test_api?sslmode=disable"
go test -p 1 ./...
```

⚠️ **必须加 `-p 1`（串行）**：`go test` 默认并行时，service 包内多个测试共用一个库、各自创建
`brain_planet` 赛项，会撞 `events_pkey` 报 8 个错。**这是测试隔离问题，不是代码缺陷** ——
遇到成片同因失败，先串行复跑确认，别急着改代码。

### 分层铁律（违反会被 review 打回）

```
cmd/server      进程装配，不写业务
internal/api    HTTP 解析 / 状态码；不写业务判断
internal/service 用例编排、事务边界、业务校验、审计埋点  ← 业务都在这
internal/store  存储契约 + 实现；不写业务
internal/engine 纯函数计算（计分/排名/脱敏/校验），零 IO
```

两条不变式：

1. **凡是改变业务数据的操作，必须与其审计记录写在同一事务里**
   —— 统一用 `s.tx(ctx, func(r store.Repos) error {...})`。
2. **`engine` 包必须零 IO**：不查库、不读文件、不碰网络。否则无法做双端逐位比对。

### 加数据库表的流程

1. 新建 `migrations/000N_xxx.{up,down}.sql`，`up` 里写清「为什么需要这张表」
2. `model` 加结构体（字段与列一一对应）
3. `store/repo.go` 加接口 → `store/postgres/` 加实现 → `tx.go` 里装配
4. `service` 调接口，**不要在 service 里写 SQL**

### ⚠️ 本仓库禁用 `git stash`

实测执行 `git stash` 会导致 `.git/refs` 目录消失（`fatal: not a git repository`）。
源码不会丢，但元数据损坏。**要暂存/建基线请用文件复制**：

```bash
cp internal/service/foo.go /tmp/foo.go.bak
```

---

## 四、改动前的自查

提交前请确认：

- [ ] 没有把真实赛事数据 / 真实姓名写进代码或演示数据
- [ ] 改了计分/排名逻辑 → 已重跑 `scripts/gen_frontend_golden.js` 更新 `testdata/frontend_golden.json`
      （脚本自带覆盖度自检，会拒绝产出不完整的基准），且 `TestParityWithFrontend*` 通过
- [ ] 新增了业务写入 → 有对应的审计埋点，且在同一事务内
- [ ] 原型改动 → CRLF 保持、`node --check` 通过、`node _全部回归_*.js` 全绿
- [ ] 后端改动 → `go build` / `go vet` / `go test -p 1 ./...` 全绿
- [ ] 文档与代码一致（README 的技术描述容易过时，改了架构记得同步）

## 五、报告问题

提 Issue 时请附上：

- 你使用的场景（哪一端、哪个页面）
- 期望行为 vs 实际行为
- 复现步骤
- 浏览器/Go 版本（涉及原型或后端时）

Bug 报告模板见 `.github/ISSUE_TEMPLATE/`。
