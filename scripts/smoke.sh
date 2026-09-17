#!/usr/bin/env bash
# ============================================================================
# 端到端冒烟：curl 串起「建赛项 → 导队伍 → 录成绩 → 出榜单 → 查审计」
#
# 用途：服务部署到赛场机器后，用它确认「真的能用」，而不是只看进程活着。
#       CI 里也可以跑（起服务 + 建库 + 执行本脚本）。
#
# 用法：
#   bash scripts/smoke.sh                          # 打默认 :8080
#   BASE_URL=http://127.0.0.1:9090/api/v1 bash scripts/smoke.sh
#
# 设计取舍：断言只看 **HTTP 状态码 + 响应体里的 "code":0**，不引入 jq 依赖。
# 赛场机器上装什么工具不可控，脚本依赖越少越跑得起来。
# ============================================================================
set -uo pipefail

BASE="${BASE_URL:-http://127.0.0.1:8080/api/v1}"
OP="${OPERATOR:-冒烟测试}"

# 临时目录放在项目内而不是系统 temp：Windows 上 mktemp 会给出混合分隔符的路径
# （C:\...\Temp/tmp.xxx），部分安全软件/回收站组件处理不了，会刷一屏无用报错。
TMP=".smoke-tmp.$$"
mkdir -p "$TMP"
trap 'rm -rf "$TMP"' EXIT

PASS=0
FAIL=0
# 用时间戳做赛项 ID，避免与已有数据冲突（冒烟脚本可以被反复执行）
EVENT="smoke_$(date +%s)"
TEAM_ID=""

blue() { printf '\033[34m%s\033[0m\n' "$*"; }
ok()   { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
bad()  { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$*"; }

# call <方法> <路径> <期望状态码> [请求体]
call() {
  local method="$1" path="$2" want="$3" body="${4:-}"
  local args=(-s -o "$TMP/body" -w '%{http_code}' -X "$method" -H "X-Operator: $OP" "$BASE$path")
  [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")

  local code
  code="$(curl "${args[@]}")" || { bad "$method $path 请求失败（服务未启动？）"; return 1; }

  if [ "$code" != "$want" ]; then
    bad "$method $path 期望 $want，实际 $code"
    printf '      响应：%s\n' "$(head -c 300 "$TMP/body")"
    return 1
  fi
  ok "$method $path → $code"
  return 0
}

# dataField <字段名>   从响应体里取第一个数字型字段值（够用即可，不引入 jq）
numField() {
  grep -o "\"$1\":[0-9]\+" "$TMP/body" | head -1 | cut -d: -f2
}

# expectBody <子串> <说明>
expectBody() {
  if grep -q -- "$1" "$TMP/body"; then
    ok "$2"
  else
    bad "$2（响应中未找到「$1」）"
    printf '      响应：%s\n' "$(head -c 300 "$TMP/body")"
  fi
}

blue "== 0. 健康检查 =="
if call GET /healthz 200; then
  expectBody '"database":"ok"' "数据库连通"
fi

blue "== 1. 建赛项（含任务项与规则）=="
call POST /events 201 "{
  \"id\": \"$EVENT\",
  \"name\": \"冒烟测试赛项\",
  \"groups\": [\"小学组\", \"初中组\"],
  \"tasks\": [
    {\"id\":\"focus\",\"name\":\"专注力任务\",\"type\":\"numeric\",\"maxScore\":100,\"weight\":0.6,\"control\":\"slider\"},
    {\"id\":\"build\",\"name\":\"搭建任务\",\"type\":\"numeric\",\"maxScore\":100,\"weight\":0.4,\"control\":\"slider\"}
  ],
  \"scoreRule\": {\"template\":\"weighted_sum\",\"params\":{}},
  \"bonusRules\": [{\"template\":\"time_bonus\",\"params\":{\"perSecond\":0.5,\"cap\":10}}],
  \"penaltyRule\": {\"template\":\"per_card\",\"params\":{\"yellow\":5,\"red\":15}},
  \"rankRule\": {\"tieBreak\":[\"score\",\"time\"],\"awardTiers\":{\"一等奖\":0.1,\"二等奖\":0.2,\"三等奖\":0.3}}
}" && expectBody "\"id\":\"$EVENT\"" "赛项已创建"

blue "== 2. 非法配置必须被拒绝 =="
call POST "/events/$EVENT/validate" 200 "{
  \"tasks\": [
    {\"id\":\"focus\",\"name\":\"A\",\"type\":\"numeric\",\"maxScore\":100,\"weight\":1},
    {\"id\":\"focus\",\"name\":\"B\",\"type\":\"numeric\",\"maxScore\":100,\"weight\":0}
  ]
}" && expectBody '"ok":false' "重复任务 id 被识别为不合法"

blue "== 3. 报名导入（预览四色 → 勾选入库）=="
ROWS='{
  "eventId": "'"$EVENT"'",
  "rows": [
    {"no":"1001","name":"星河队","school":"杭州实验小学","coach":"张老师","group":"小学组","members":"张一 / 李二"},
    {"no":"1002","name":"追光队","school":"杭州第二实验小学","coach":"孙老师","group":"小学组","members":"王三 / 赵四"},
    {"no":"1003","name":"晨曦队","school":"杭州实验小学","coach":"李老师","group":"初中组","members":"孙五 / 周六"},
    {"no":"1003","name":"晨曦队重复行","group":"初中组"},
    {"no":"1004","name":"组别错误队","group":"高中组"}
  ]
}'
call POST /imports/preview 200 "$ROWS" && {
  expectBody '"insert":3' "识别出 3 支新增"
  expectBody '"conflict":2' "识别出 2 条冲突（编号重复 + 组别不属于本赛项）"
}
call POST /imports/commit 201 '{
  "eventId": "'"$EVENT"'",
  "rows": [
    {"no":"1001","name":"星河队","school":"杭州实验小学","coach":"张老师","group":"小学组","members":"张一 / 李二"},
    {"no":"1002","name":"追光队","school":"杭州第二实验小学","coach":"孙老师","group":"小学组","members":"王三 / 赵四"},
    {"no":"1003","name":"晨曦队","school":"杭州实验小学","coach":"李老师","group":"初中组","members":"孙五 / 周六"}
  ],
  "selectedLines": [1, 2, 3],
  "note": "冒烟测试导入"
}' && expectBody '"detail"' "导入审计已生成"

blue "== 4. 读队伍，拿数据库 ID =="
if call GET "/events/$EVENT/teams" 200; then
  TEAM_ID="$(numField id)"
  if [ -n "$TEAM_ID" ]; then ok "队伍 ID = $TEAM_ID"; else bad "未取到队伍 ID"; fi
fi

blue "== 5. 录成绩（两轮取优）=="
call PUT "/teams/$TEAM_ID/scores/1" 200 '{"tasks":{"focus":80,"build":82},"time":100,"signed":true}' &&
  expectBody '"teamId":'"$TEAM_ID" "第一轮录入成功"
call PUT "/teams/$TEAM_ID/scores/2" 200 '{"tasks":{"focus":95,"build":92},"time":90,"signed":true}' &&
  expectBody '"signed":true' "第二轮录入成功"

blue "== 6. 已签字成绩禁止直接改 =="
call PUT "/teams/$TEAM_ID/scores/2" 409 '{"tasks":{"focus":100,"build":100},"time":90,"signed":true}' &&
  expectBody '改分申请' "409 提示引导走改分申请"

blue "== 7. 改分：申请（只留痕）→ 裁判长授权后落库 =="
call POST "/teams/$TEAM_ID/scores/2/change-requests" 200 \
  '{"after":99,"reason":"申诉复核：搭建任务漏计 1 个构件"}' &&
  expectBody '"approved":false' "申请未自动获批"
call POST "/teams/$TEAM_ID/scores/2/apply-change" 200 '{
  "tasks":{"focus":100,"build":92},"time":90,"signed":true,
  "reason":"申诉成立，授权修改","approver":"裁判长C"
}' && ok "授权改分已落库"

blue "== 8. 出榜单（按组别）=="
if call GET "/events/$EVENT/standings" 200; then
  expectBody '"group":"小学组"' "按组别分组返回"
  expectBody '"bestRound":2' "两轮取优生效"
fi

blue "== 9. 大屏（姓名已脱敏）=="
if call GET "/screen/$EVENT" 200; then
  if grep -q '"members":"[^"]*\*' "$TMP/body"; then
    ok "选手姓名已脱敏"
  else
    bad "大屏未脱敏"
    printf '      响应：%s\n' "$(head -c 300 "$TMP/body")"
  fi
  if grep -q '张一\|李二' "$TMP/body"; then
    bad "大屏泄露了真实姓名"
  else
    ok "未泄露真实姓名"
  fi
fi

blue "== 10. 六类操作留痕（导入 + 改分配置已产生）=="
if call GET /audit-summary 200; then
  expectBody '"required"' "返回必须留痕的动作清单"
  expectBody '"counts"' "返回留痕条数统计"
fi
call GET "/audit-logs?approver=裁判长C" 200 && expectBody '"approver":"裁判长C"' "可按审批人检索审计"

blue "== 11. 未知接口返回 JSON 404（而不是纯文本）=="
call GET /不存在的端点 404
if grep -q '"code"' "$TMP/body" 2>/dev/null; then
  ok "404 也是 JSON"
else
  bad "404 响应不是 JSON"
fi

echo
if [ "$FAIL" -eq 0 ]; then
  printf '\033[32m全部通过：%d 项断言\033[0m\n' "$PASS"
  exit 0
fi
printf '\033[31m失败 %d 项 / 通过 %d 项\033[0m\n' "$FAIL" "$PASS"
exit 1
