#!/usr/bin/env node
/**
 * 生成「前端对照基准」（P2 验收工具）
 *
 * 背景：后端 engine 包是按前端原型 `赛事统分后台管理_demo.html` 里的
 * computeTotal / standings / maskPerson / maskMembers / validateEvent
 * 逐一移植的。移植是否忠实，不能靠肉眼看代码，必须拿同一份输入跑出结果来比。
 *
 * 做法：从 demo HTML 里**原样抽取**前端实现（不是复制粘贴一份到本文件，
 * 否则前端改了这里不会跟着变），用同一份输入算出期望值，输出
 * testdata/frontend_golden.json；再由 Go 单测读这份基准做逐位断言。
 * 这样一来，前端一旦改了算法，重跑本脚本 + go test 就能立刻发现两端分叉。
 *
 * ---------------------------------------------------------------------------
 * ⚠️ 2026-10-10 重写（上一版已彻底跑不通，教训见下）
 *
 * 上一版写死了要抽的函数名清单（clone / seedData / …）。demo 迭代后
 * `seedData()` 整个消失（多赛事改造后变成 blankContestData()），
 * `standings()` 又新长出 rankAll / bestRoundOf / effectiveRed 等一大串依赖，
 * 于是脚本**崩在抽取阶段** —— 而它崩掉是不报错的：`TestParityWithFrontend*`
 * 仍读着 09-17 的旧文件，两边都是旧快照、依然自洽，于是测试一直绿。
 * 「前端改了算法 → 重跑脚本即可发现分叉」这条自检链路就是这样**静默断掉**的。
 *
 * 所以本版把两处根因都改掉：
 *
 *  1. **函数/常量清单不再手写**：从入口出发自动展开依赖闭包（见 §1）。
 *     前端新长出一个 helper，这里自动跟着长，不会再「因为清单过时」而崩。
 *  2. **输入不再依赖 demo 的种子数据**：demo 的种子是"演示用假数据"、
 *     随界面改动而变；基准要的是"覆盖分支的测试输入"，本就该自己显式定义。
 *     赛项配置仍从 demo 原样抽取（EVENT_TEMPLATES），所以**配置口径永远跟着前端走**；
 *     队伍与成绩则是显式 fixture（见 §2），并在 §4 自检覆盖率。
 *
 * 用法：
 *   node scripts/gen_frontend_golden.js [demo.html 路径] [输出路径]
 */
'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const DEFAULT_DEMO = 'D:/Desktop/workbuddy/赛事统分后台管理_demo.html';

const demoPath = process.argv[2] || DEFAULT_DEMO;
const outPath =
  process.argv[3] || path.join(__dirname, '..', 'testdata', 'frontend_golden.json');

// ===========================================================================
// 1. 抽取：自动依赖闭包
// ===========================================================================

/** 取出 HTML 里所有内联 <script> 的内容（跳过带 src 的）。 */
function extractInlineScript(html) {
  const re = /<script(?![^>]*\ssrc=)[^>]*>([\s\S]*?)<\/script>/gi;
  const parts = [];
  let m;
  while ((m = re.exec(html))) parts.push(m[1]);
  if (!parts.length) throw new Error('demo 里找不到内联 <script>');
  return parts.join('\n;\n');
}

// —— 极简 JS 词法跳过 ——
//
// 抽取器要"数括号"，就不能把字符串 / 注释 / **正则字面量**里的括号当代码。
// 正则字面量是最容易漏的一个：demo 里有 `replace(/'/g,'')` 这种写法，
// 不认它，扫描器会把正则里的那个引号当成字符串起点，一路错位到后面几行，
// 于是花括号配平失败 —— **而报错位置离真正的病灶很远**（实测差了 1600 字符），
// 排查成本极高。所以这里统一成一个跳过器，三处消费者共用。
//
const IDENT_CH = /[A-Za-z0-9_$]/;
const REGEX_KEYWORDS = new Set([
  'return', 'typeof', 'instanceof', 'in', 'of', 'new', 'delete',
  'void', 'case', 'do', 'else', 'yield', 'await',
]);

/** `/` 在此处能否作为正则字面量的开头（否则是除号）。 */
function regexAllowed(src, i) {
  let k = i - 1;
  while (k >= 0 && /\s/.test(src[k])) k--;
  if (k < 0) return true;
  const p = src[k];
  if (p === ')' || p === ']') return false; // (a)/(b)、arr[i]/2 之类多为除号
  if (!IDENT_CH.test(p)) return true; // 运算符 / 括号 / 逗号之后 → 正则
  let e = k;
  while (e >= 0 && IDENT_CH.test(src[e])) e--;
  return REGEX_KEYWORDS.has(src.slice(e + 1, k + 1));
}

/** 跳过正则字面量（含字符类与结尾 flags）；判断错误时返回 -1。 */
function skipRegex(src, i) {
  let j = i + 1;
  let inClass = false;
  while (j < src.length) {
    const c = src[j];
    if (c === '\\') {
      j += 2;
      continue;
    }
    if (c === '\n') return -1; // 正则不跨行 → 前面判错了
    if (inClass) {
      if (c === ']') inClass = false;
      j++;
      continue;
    }
    if (c === '[') {
      inClass = true;
      j++;
      continue;
    }
    if (c === '/') {
      j++;
      break;
    }
    j++;
  }
  while (j < src.length && /[a-z]/.test(src[j])) j++; // flags
  return j;
}

/**
 * 从 i 起跳过一个「不参与语法」的片段。
 * 返回 {end, kind}：kind 为 null 表示 src[i] 就是普通代码字符（end === i）。
 */
function skipInvisible(src, i) {
  const c = src[i];
  const n = src[i + 1];
  if (c === '/' && n === '/') {
    let j = i;
    while (j < src.length && src[j] !== '\n') j++;
    return { end: j, kind: 'comment' };
  }
  if (c === '/' && n === '*') {
    let j = i + 2;
    while (j < src.length && !(src[j] === '*' && src[j + 1] === '/')) j++;
    return { end: Math.min(src.length, j + 2), kind: 'comment' };
  }
  if (c === '"' || c === "'" || c === '`') {
    let j = i + 1;
    while (j < src.length) {
      if (src[j] === '\\') {
        j += 2;
        continue;
      }
      if (src[j] === c) {
        j++;
        break;
      }
      if (src[j] === '\n' && c !== '`') break; // 未闭合：就地止损，避免吞掉整份文件
      j++;
    }
    return { end: j, kind: 'string' };
  }
  if (c === '/' && regexAllowed(src, i)) {
    const j = skipRegex(src, i);
    if (j > 0) return { end: j, kind: 'regex' };
  }
  return { end: i, kind: null };
}

/**
 * 把字符串 / 模板串 / 注释 / 正则字面量替换成等长空白（换行保留）。
 * 只在它上面扫标识符 —— 否则注释与文案里的词会被当成引用，
 * 把闭包撑成"什么都依赖"。
 */
function maskLiterals(src) {
  const out = new Array(src.length);
  let i = 0;
  while (i < src.length) {
    const inv = skipInvisible(src, i);
    if (inv.kind) {
      for (let k = i; k < inv.end; k++) out[k] = src[k] === '\n' ? '\n' : ' ';
      i = inv.end;
      continue;
    }
    out[i] = src[i];
    i++;
  }
  return out.join('');
}

/** 从 fromIndex（某个 { / ( / [ 处）配平到对应的收尾符，返回收尾符下标。 */
function matchBracket(src, fromIndex) {
  const open = src[fromIndex];
  const close = open === '{' ? '}' : open === '(' ? ')' : ']';
  let depth = 0;
  let i = fromIndex;
  while (i < src.length) {
    const inv = skipInvisible(src, i);
    if (inv.kind) {
      i = inv.end;
      continue;
    }
    const c = src[i];
    if (c === open) depth++;
    else if (c === close) {
      depth--;
      if (depth === 0) return i;
    }
    i++;
  }
  throw new Error('括号未配平，起始下标 ' + fromIndex);
}

/** 收集所有顶格（列 0）的 function 声明；重名时后者覆盖并记入 dupes。 */
function collectFunctions(src) {
  const map = new Map();
  const dupes = [];
  const re = /^function\s+([A-Za-z_$][\w$]*)\s*\(/gm;
  let m;
  while ((m = re.exec(src))) {
    const start = m.index;
    const paren = src.indexOf('(', start);
    if (paren < 0) throw new Error('参数列表缺失: ' + m[1]);
    const rparen = matchBracket(src, paren); // 参数列表里也可能有花括号（解构）
    const brace = src.indexOf('{', rparen);
    if (brace < 0) throw new Error('函数体缺失: ' + m[1]);
    const end = matchBracket(src, brace);
    if (map.has(m[1])) dupes.push(m[1]);
    map.set(m[1], { text: src.slice(start, end + 1), index: start });
  }
  return { map, dupes };
}

/** 收集所有顶格（列 0）的 var 声明。缩进的 var 是函数内局部变量，不算。 */
function collectTopVars(src) {
  const map = new Map();
  const re = /^var\s+([A-Za-z_$][\w$]*)\s*=/gm;
  let m;
  while ((m = re.exec(src))) {
    const semi = findStatementEnd(src, m.index);
    map.set(m[1], { text: src.slice(m.index, semi + 1), index: m.index });
  }
  return map;
}

/** 从 k 起找语句结束的 ;（跳过括号内的分号与字符串 / 注释 / 正则）。 */
function findStatementEnd(src, k) {
  let i = k;
  let depth = 0;
  while (i < src.length) {
    const inv = skipInvisible(src, i);
    if (inv.kind) {
      i = inv.end;
      continue;
    }
    const c = src[i];
    if (c === '(' || c === '[' || c === '{') depth++;
    else if (c === ')' || c === ']' || c === '}') depth--;
    else if (c === ';' && depth === 0) return i;
    i++;
  }
  throw new Error('语句未以分号结束，起始下标 ' + k);
}

/** 在掩码后的文本上取被引用的标识符（去掉属性访问与对象字面量的键）。 */
function referencedIdentifiers(masked) {
  const cleaned = masked
    .replace(/\.\s*[A-Za-z_$][\w$]*/g, (s) => ' '.repeat(s.length))
    .replace(/([{,]\s*)([A-Za-z_$][\w$]*)\s*:/g, (s, p1) => p1 + ' '.repeat(s.length - p1.length));
  const out = new Set();
  const re = /[A-Za-z_$][\w$]*/g;
  let m;
  while ((m = re.exec(cleaned))) out.add(m[0]);
  return out;
}

/**
 * 从入口函数出发，展开依赖闭包。
 * 只认**本文件里定义的** function 与 var —— 其它标识符（document / alert / 内建）
 * 不会被收进来，需要的话由调用方显式 stub。
 */
function closureOf(fns, vars, entries) {
  const needFns = new Set();
  const needVars = new Set();
  const queue = [];

  for (const e of entries) {
    const def = fns.get(e);
    if (!def) throw new Error('抽不到入口函数（demo 里已改名或删除？）: ' + e);
    needFns.add(e);
    queue.push(def.text);
  }

  while (queue.length) {
    const ids = referencedIdentifiers(maskLiterals(queue.shift()));
    for (const id of ids) {
      if (needFns.has(id) || needVars.has(id)) continue;
      if (fns.has(id)) {
        needFns.add(id);
        queue.push(fns.get(id).text);
      } else if (vars.has(id)) {
        needVars.add(id);
        queue.push(vars.get(id).text);
      }
    }
  }
  return { needFns, needVars };
}

const html = fs.readFileSync(demoPath, 'utf8');
const script = extractInlineScript(html);

const { map: FN, dupes: FN_DUPES } = collectFunctions(script);
const VAR = collectTopVars(script);

// 入口：计分 / 排名 / 脱敏 / 配置校验
const ENTRIES = ['computeTotal', 'standings', 'maskPerson', 'maskMembers', 'validateEvent'];
const { needFns, needVars } = closureOf(FN, VAR, ENTRIES);

/**
 * 显式要求的常量。
 *
 * 闭包只认"被函数引用到的"东西，而 EVENT_TEMPLATES 没有任何入口函数会读它
 * （算分只看传进去的 ev）—— 但 §2 的输入构造要靠它，它是赛项配置的事实源。
 * 所以在这里点名要求：抽不到就报错，不静默降级成"配置为空"。
 */
const REQUIRED_CONSTS = ['EVENT_TEMPLATES'];
for (const n of REQUIRED_CONSTS) {
  if (!VAR.has(n)) {
    throw new Error('demo 里找不到常量 ' + n + '（赛项配置的事实源，不能缺）');
  }
  needVars.add(n);
}

/** 需要显式 stub 的浏览器全局。出现新的一个就让脚本崩，再显式补上 —— 不静默跳过。 */
const BROWSER_GLOBALS = /\b(document|window|localStorage|sessionStorage|navigator|location|alert|confirm|prompt|FileReader|Blob|XMLHttpRequest|performance)\b/;

/**
 * 闭包里被用到的顶层 var：初始化阶段不能碰浏览器 API。
 * 一旦命中就**报错**而不是跳过 —— 上个版本就是"悄悄跳过"把问题藏起来的。
 */
const varDecls = [...needVars]
  .map((n) => ({ n, def: VAR.get(n) }))
  .sort((a, b) => a.def.index - b.def.index);
for (const { n, def } of varDecls) {
  const hit = maskLiterals(def.text).match(BROWSER_GLOBALS);
  if (hit) {
    throw new Error(
      '闭包用到的顶层常量 ' + n + ' 在初始化时依赖浏览器 API（' + hit[0] + '）。\n' +
        '  基准脚本跑在 Node 里，没有这些对象。请在脚本里为它显式提供 stub，' +
        '或把该依赖从闭包里排除（不要静默跳过）。'
    );
  }
}
const varText = varDecls.map((v) => v.def.text).join('\n');
const fnText = [...needFns].map((n) => FN.get(n).text).join('\n\n');

/**
 * 把闭包编译成「给定 S 就能算」的 API。
 *
 * 只注入一个参数 S：闭包里被用到的外部全局仅此一个（浏览器 API 已在上面拦掉）。
 * 其余标识符要么是闭包内定义的函数/常量，要么是 JS 内建（Math / JSON / Object…）。
 */
function makeApi(S) {
  const body =
    varText +
    '\n\n' +
    fnText +
    '\n\nreturn {computeTotal, standings, maskPerson, maskMembers, validateEvent, eventTotalSec,' +
    ' REF_TIME, EVENT_TEMPLATES};\n';
  return new Function('S', body)(S);
}

// —— 阶段 A：先只取常量（EVENT_TEMPLATES 就是赛项配置的事实源）——
const stageA = makeApi(undefined);
const REF_TIME = stageA.REF_TIME;
const TEMPLATES = stageA.EVENT_TEMPLATES;

if (typeof REF_TIME !== 'number') throw new Error('抽到的 REF_TIME 不是数字：' + REF_TIME);
if (!Array.isArray(TEMPLATES) || !TEMPLATES.length) throw new Error('抽不到 EVENT_TEMPLATES');

// ===========================================================================
// 2. 输入 fixture
// ===========================================================================
//
// 显式定义，不依赖 demo 的演示种子数据。理由见文件头。
//
// 队伍画像：每条覆盖一类分支。任务取值按赛项**自身的任务清单**生成，
// 不硬编码 task id —— 赛项改了任务，这里不用跟着改。
//
const PROFILES = {
  full_fast: { fill: 1, tf: 0.3, note: '满分·远快于基准（时间奖励封顶）' },
  full_near: { fill: 1, tf: 0.95, note: '满分·接近基准用时（时间奖励未封顶）' },
  full_over: { fill: 1, tf: 1.2, note: '满分·超基准用时（时间奖励按 0 计）' },
  half: { fill: 0.5, tf: 0.6, note: '半分·完整录入' },
  partial: { fill: 0.8, tf: 0.6, firstOnly: true, note: '只录第一个任务（未完成）' },
  empty_tasks: { fill: 0, tf: 0.5, noTasks: true, note: '有记录但任务全空' },
  no_record: { noRecord: true, note: '无成绩记录（未开赛）' },
  yellow2: { fill: 0.8, tf: 0.7, yellow: 2, note: '黄牌 2 张（未达阈值，不升级）' },
  yellow3: {
    fill: 0.9,
    tf: 0.5,
    upgradedRed: 1,
    note: '黄牌已记满阈值 → 录入端即升级为红牌（存的是升级后的形态：黄牌计数清零 + 升级红牌 1）→ 取消资格',
  },
  red1: { fill: 0.95, tf: 0.85, red: 1, note: '直接红牌 1 张 → 取消资格' },
  withdraw: { fill: 0.85, tf: 0.8, withdrawn: true, note: '弃赛（不进榜单）' },
  void_case: { fill: 0.7, tf: 0.7, voided: true, note: '裁定取消资格（成绩作废、整行不进榜单）' },
  tie_a: { fill: 0.6, tf: 0.75, note: '与 tie_b 完全同分同用时 → 并列' },
  tie_b: { fill: 0.6, tf: 0.75, note: '与 tie_a 完全同分同用时 → 并列' },
};

/** 两轮制专用画像：r 给每轮各自的填充比与用时比。 */
const ROUND_PROFILES = {
  r_better2: { r: { 1: { fill: 0.5, tf: 0.6 }, 2: { fill: 1, tf: 0.5 } }, note: '第 1 轮半分、第 2 轮满分 → 取第 2 轮' },
  r_better1: { r: { 1: { fill: 1, tf: 0.6 }, 2: { fill: 0.4, tf: 0.4 } }, note: '第 1 轮满分、第 2 轮低分 → 取第 1 轮' },
  r_only1: { r: { 1: { fill: 0.7, tf: 0.7 } }, note: '只有第 1 轮有记录' },
  r_only2: { r: { 2: { fill: 0.9, tf: 0.6 } }, note: '只有第 2 轮有记录' },
  r_time2: { r: { 1: { fill: 0.8, tf: 0.5 }, 2: { fill: 0.8, tf: 0.4 } }, note: '两轮总分相同、第 2 轮用时更少 → 取第 2 轮' },
  r_same: { r: { 1: { fill: 0.8, tf: 0.6 }, 2: { fill: 0.8, tf: 0.6 } }, note: '两轮总分与用时都相同 → 取轮次小者' },
  r_no_rec: { noRecord: true, note: '两轮都无记录' },
};

const NAMES = [
  '星河队',
  '追光队',
  '晨曦队',
  '先锋队',
  '极光队',
  '长风队',
  '破晓队',
  '山海队',
  '苍穹队',
  '数智队',
  '拓荒队',
  '微光队',
  '曙光队',
  '星海队',
];

/** 按赛项任务清单生成一份「填充比 fill」的任务取值。 */
function taskValues(ev, spec) {
  const st = {};
  const tasks = ev.tasks || [];
  tasks.forEach((t, i) => {
    if (spec.noTasks) return;
    if (spec.firstOnly && i > 0) return;
    st[t.id] = valueFor(t, spec.fill);
  });
  // count_bonus 引用的 id 不一定在任务清单里（现场确有这种配置：
  // 奖励挂在一个只用于计数的道具上）。这类 id 也要给值，否则该加分分支恒为 0、
  // 等于没覆盖。
  (ev.bonusRules || []).forEach((b) => {
    const tid = b.params && b.params.taskId;
    if (b.template !== 'count_bonus' || !tid || st[tid] !== undefined) return;
    if (spec.noTasks) return;
    st[tid] = Math.round(6 * (spec.fill === undefined ? 1 : spec.fill));
  });
  return st;
}

function valueFor(t, fill) {
  const max = Number(t.maxScore) || 0;
  if (t.type === 'toggle') return fill >= 1;
  if (t.type === 'enum') {
    const ks = Object.keys(t.enumMap || {});
    if (!ks.length) return '';
    const idx = Math.min(ks.length - 1, Math.max(0, Math.ceil(fill * ks.length) - 1));
    return ks[idx];
  }
  if (t.type === 'count') {
    const w = Number(t.weight) || 1;
    return Math.round((max / w) * fill);
  }
  return Math.round(max * fill * 100) / 100;
}

/**
 * 用基准时长把「用时比」换成秒。
 * 口径必须与前端 eventTotalSec 一致：先累加各阶段时长；没有阶段才看
 * scoreRule.params.refTime；都没有才用 REF_TIME。
 * 若这里与前端不一致，时间奖励那一项就会整体偏掉，且看不出是哪一侧错。
 */
function secondsFor(ev, tf) {
  let total = 0;
  (ev.phases || []).forEach((p) => {
    total += Number(p.durationSec) || 0;
  });
  if (!total) {
    const v = ev.scoreRule && ev.scoreRule.params ? ev.scoreRule.params.refTime : null;
    if (v != null && v !== '' && Number(v) > 0) total = Number(v);
  }
  if (!total) total = REF_TIME;
  return Math.round(total * tf);
}

function buildScenario(scn) {
  const ev = JSON.parse(JSON.stringify(scn.event));
  const teams = [];
  const scores = {};
  const dqCases = [];
  let id = 0;

  scn.plan.forEach((entry) => {
    const p = entry.spec;
    id++;
    const name = NAMES[(id - 1) % NAMES.length];
    const team = {
      id,
      eventId: ev.id,
      no: scn.noPrefix + String(1000 + id),
      name: entry.name || name,
      group: scn.groups[(id - 1) % scn.groups.length],
      school: '示例学校' + ((id % 4) || 4),
      coach: '教练' + id,
      members: '选手甲',
      status: p.withdrawn ? 'withdrawn' : 'active',
      note: p.note,
    };
    teams.push(team);
    if (p.voided) {
      dqCases.push({ teamId: id, status: '已生效', reason: '复核实锤（基准样例）' });
    }
    if (p.noRecord) return;

    const rounds = p.r ? Object.keys(p.r).map(Number) : [1];
    scores[id] = {};
    rounds.forEach((rn) => {
      const spec = p.r ? p.r[rn] : p;
      scores[id][rn] = {
        roundNo: rn,
        tasks: taskValues(ev, spec),
        time: secondsFor(ev, spec.tf),
        // 黄牌计数与升级红牌都按「录入端归一后的形态」给值：
        // 记满阈值的那部分在录入时就被折算成 upgradedRed、计数清零
        // （见 engine.NormalizeCards）。给「yellow=3 且 upgradedRed=0」这种
        // 录入端永远不会产生的状态，会让基准与真实数据流对不上。
        yellow: Number(spec.yellow) || 0,
        red: Number(spec.red) || 0,
        upgradedRed: Number(spec.upgradedRed) || 0,
        signed: true,
      };
    });
  });

  const slots = (scn.slots || []).map((s, i) => ({
    id: i + 1,
    seatId: 1,
    period: i === 0 ? '上午' : '下午',
    round: s,
    eventId: ev.id,
    group: scn.groups[0],
  }));

  return {
    events: [ev],
    teams,
    scores,
    dqCases,
    // 赛事级递补规则：与前端 S.substituteRule 同结构（mode + note）
    substituteRule: { mode: scn.substitute, note: '' },
    slots,
  };
}

// —— 场景清单 ——
// 「真实配置」组：直接取 EVENT_TEMPLATES，前端改配置这里跟着变。
// 「兼容分支 / 边界」组：在真实赛项上打补丁，覆盖现配置碰不到的代码路径。
//
/**
 * 递补规则：前端有赛事级开关 `substituteRule.mode`，**默认 `'none'`（不递补）**：
 * 被裁定「取消资格」的队伍留下的名次位置**留空**，后面的队伍保留原名次
 * —— 公示表上就是 4 → 6 这种空洞。
 *
 * 2026-10-10 之前后端只有「名次连续发放」一种行为，这条口径只能**排除在对照之外**
 * （原因当时记在下面的 excluded 里）。现在后端补上了赛事级规则
 * `contest_rules.substitute_mode` 与 `engine.RankOptions.KeepGap`，两档都能跑，于是：
 *
 *   - 主场景一律用**产品默认的 `'none'`** —— 基准要在"现场实际在用的口径"下对照；
 *   - 另有 `brain_planet__substitute_rank` 场景覆盖 `'rank'` 那一档。
 *
 * 覆盖度自检会要求两档各至少有一个场景，且它们**都留着作废队**
 * —— 没有作废队时这两档结果完全一样，那样的对照是空的。
 */
const SUBSTITUTE_DEFAULT = 'none';

const EXCLUDED = [
  {
    affects: 'cards',
    what: '「红牌落在非取优轮」的取消资格判定（2 轮赛项：第 1 轮红牌、第 2 轮干净且更优）',
    why:
      '两端口径目前相反：前端 rankAll 取的是**取优那一轮**的 computeTotal().dq' +
      '（第 2 轮干净 → 不取消资格），后端 DisqualifiedByCards 扫**全部轮次**记录' +
      '（任一红牌 → 取消资格）。这不是实现疏忽，而是产品语义未定：' +
      '「红牌取消比赛资格」是**整场**还是**仅该轮**（详见 PROGRESS 待办）。' +
      '语义确定前不构造用例 —— 否则测试会逼着其中一端去迁就另一端，把问题掩盖掉。',
  },
  {
    affects: '',
    what: 'per_card（历史扣分分支）+ 黄牌记满升级：扣分基数是「累计张数」还是「升级后余数」',
    why:
      '两端口径不同且**都说得通**：前端 computeTotal 的 per_card 扣分直接读 rec.yellow（录入时的原始值），' +
      '3 张黄牌 = 3 × 5 = 15 分；后端在 SaveScore 时已按 NormalizeCards 把黄牌清零、折成 upgraded_red，' +
      '再算扣分就成了 0 × 5 = 0 分（升级出的红牌不计入 per_card）。' +
      '由于现配置的三个赛项全是 record_only（判罚不扣分），这条对现网没有影响；' +
      '只有在读历史 per_card 赛事时才会显现，因此不构造用例，等真要用这条分支时再定口径。',
  },
  {
    affects: 'mask',
    what: '姓名脱敏的输出（MaskPerson / MaskMembers）',
    why:
      '三处实现互不相同，且都不等于裁判端设计册的口径（设计册写的是「卫*九」= 保留首末字 + 星号填充）：' +
      'demo 现状 = 保留首末字 + 小写 x 填充；web/index.html 与两份根 index.html = 只留首字 + 星号（上限 2 个）；' +
      'Go = 只留首字 + 星号（上限 2 个，与 web 一致、与 demo 不一致）。' +
      '这是**产品口径问题**（公示表 / 大屏上看得见），不是引擎算法分歧，' +
      '改一处就会让另两处继续不一致，因此不在基准里对照，等口径统一后再打开。' +
      'MaskPerson 的逐位对照目前只由 engine 自身单测守护。',
  },
  {
    affects: 'award',
    what: '奖项名额分配（含「作废队是否占名额」这条口径）',
    why:
      '前端**没有奖项计算**：demo 里 award / 获奖 / 等奖 词频全为 0，' +
      'EVENT_TEMPLATES 的 rankRule 只有 tieBreak、没有 awardTiers，' +
      '所以这份基准的 award 字段在现配置下恒为空串 —— 两侧无从对照。' +
      '口径（2026-10-10 定案）：作废队在 assignAwards 之前整行剔除，既不参与名额的分母' +
      '（名额 = floor(在榜队数 × 占比)）也不当获奖人，让出的名额由后面队伍顶上；' +
      '注意这与默认的「不递补」不矛盾 —— 名次上的空洞保留、奖项上的名额不留。' +
      '该口径只由 engine 单测守护：TestRankVoidedTeamFreesAwardSlot、TestRankAwardAssignment、' +
      'TestRankAwardsUseFloorNotCeil、TestRankAwardOnlyComplete。' +
      '登记在此的目的是：别把「基准里奖项目录全空」当成「奖项已对齐」。',
  },
];

const ALL = Object.keys(PROFILES);

function planOf(keys) {
  return keys.map((k) => ({ spec: PROFILES[k] }));
}

const SCENARIOS = [
  {
    id: 'brain_planet',
    note: '真实配置：脑机星球（record_only · time_bonus）',
    index: 0,
    plan: planOf(ALL),
  },
  {
    id: 'mars_rescue',
    note: '真实配置：火星救援（record_only · count_bonus ×2 + time_bonus）',
    index: 1,
    plan: planOf(ALL),
  },
  {
    id: 'future_city',
    note: '真实配置：未来之城（多阶段 / toggle / count / 阶段总时长作基准）',
    index: 2,
    plan: planOf(ALL),
  },
  {
    id: 'brain_planet__per_card',
    note: '兼容分支：per_card 按牌扣分（现配置已不用，但代码分支还在）',
    index: 0,
    patch: (e) => {
      e.penaltyRule = { template: 'per_card', params: { yellow: 5, red: 15 } };
    },
    plan: planOf(['yellow2', 'yellow3', 'red1', 'half', 'full_fast', 'withdraw']),
  },
  {
    id: 'brain_planet__card_counter_off',
    note: '边界：关闭黄牌计数器 → 黄牌不升级（开关真的不生效，不只是藏起界面）',
    index: 0,
    patch: (e) => {
      e.penaltyRule.cardRules.enabled = false;
    },
    plan: planOf(['yellow2', 'yellow3', 'red1', 'half']),
  },
  {
    id: 'brain_planet__no_bonus',
    note: '边界：无任何加分规则 → bonus 恒为 0',
    index: 0,
    patch: (e) => {
      e.bonusRules = [];
    },
    plan: planOf(['full_fast', 'half', 'empty_tasks', 'no_record']),
  },
  {
    id: 'mars_rescue__count_cap',
    note: '边界：计数奖励封顶（cap 生效）',
    index: 1,
    patch: (e) => {
      e.bonusRules = [
        { template: 'count_bonus', params: { taskId: 'energy', perUnit: 5, cap: 15 } },
        { template: 'count_bonus', params: { taskId: 'bridge', perUnit: 5, cap: 15 } },
      ];
    },
    plan: planOf(['full_fast', 'half']),
  },
  {
    id: 'brain_planet__substitute_rank',
    note: '名次编排：按名次顺延（作废队的位置由后面队伍顶上，编号连续）',
    index: 0,
    substitute: 'rank',
    plan: planOf(['full_fast', 'half', 'yellow2', 'red1', 'withdraw', 'void_case', 'tie_a', 'tie_b']),
  },
  {
    id: 'brain_planet__rounds2',
    note: '两轮制：取优（总分 ↓ → 用时 ↑ → 轮次小者）',
    index: 0,
    patch: (e) => {
      e.rounds = 2;
    },
    slots: [1, 2],
    roundPlan: true,
  },
];

// 记录源文件的指纹：Go 侧据此判断"基准是不是已经过期"。
//
// 这份基准曾静默脱离前端近一个月（脚本失效 → 两边都是旧快照 → 测试照样绿），
// 所以必须留下一个能被自动检查的新鲜度凭证，而不是只写一个 generatedAt。
const sourceRaw = fs.readFileSync(demoPath);
const golden = {
  source: path.basename(demoPath),
  sourceInfo: {
    name: path.basename(demoPath),
    bytes: sourceRaw.length,
    sha256: crypto.createHash('sha256').update(sourceRaw).digest('hex'),
    mtime: fs.statSync(demoPath).mtime.toISOString(),
  },
  note:
    '由 scripts/gen_frontend_golden.js 从 demo 前端实现自动生成，请勿手工编辑。' +
    '赛项配置取自 demo 的 EVENT_TEMPLATES；队伍与成绩为脚本内显式 fixture（覆盖分支用）',
  generatedAt: new Date().toISOString(),
  refTime: REF_TIME,
  closure: {
    entrypoints: ENTRIES,
    functions: [...needFns].sort(),
    vars: varDecls.map((v) => v.n),
  },
  scenarios: [],
  // 明确"没在对照什么"：否则下次有人照着基准读，会以为它覆盖了全部口径
  excluded: EXCLUDED,
  events: [],
  maskPersonCases: [],
  maskCases: [],
};

SCENARIOS.forEach((scn, si) => {
  const event = JSON.parse(JSON.stringify(TEMPLATES[scn.index]));
  if (scn.patch) scn.patch(event);
  event.id = scn.id;

  const eff = {
    event,
    plan: scn.roundPlan
      ? Object.keys(ROUND_PROFILES).map((k) => ({ spec: ROUND_PROFILES[k] }))
      : scn.plan,
    groups: event.groups && event.groups.length ? event.groups : ['小学组'],
    // 队号必须**纯数字**（后端强校验），且**赛事内唯一** ——
    // 所以前缀取场景序号（而不是模板序号：同一模板会派生多个场景，会撞号）。
    noPrefix: String(si + 1),
    slots: scn.slots,
    // 名次编排口径：主场景用产品默认（不递补），个别场景显式覆盖另一档
    substitute: scn.substitute || SUBSTITUTE_DEFAULT,
  };

  const S = buildScenario(eff);
  golden.scenarios.push({ id: scn.id, note: scn.note, teams: S.teams.length });

  const api = makeApi(S);
  const feValidate = api.validateEvent(event);
  const item = {
    id: scn.id,
    note: scn.note,
    // 本赛项的时间奖励基准时长（前端 eventTotalSec 的结论）：
    // 多阶段赛项 = 各阶段之和（未来之城 225s），单阶段/无阶段 = params.refTime 或 REF_TIME。
    // Go 侧必须用 engine.RefTimeFor 自行推导出同一个数 —— 这是**逐位对照的一部分**，
    // 不能拿全局 refTime 顶替（那正是"后端按 120s 算时间奖励"这个 bug 的温床）。
    refTime: api.eventTotalSec(event),
    // 本场景生效的名次编排口径（前端 S.substituteRule.mode 的取值）。
    // Go 侧据此决定 engine.RankOptions.KeepGap —— 基准因此能覆盖"名次留空"这一档。
    substituteMode: S.substituteRule.mode,
    event,
    teams: [],
    standings: [],
    frontendValidation: { errs: feValidate.errs || [], warns: feValidate.warns || [] },
  };

  S.teams.forEach((t) => {
    const recs = S.scores[t.id] || {};
    const order = Object.keys(recs)
      .map(Number)
      .sort((a, b) => a - b);
    // 逐轮各算一遍：于是「某一轮算错」不会被取优掩盖掉。
    const perRound = order.map((rn) => {
      const c = api.computeTotal(event, recs[rn]);
      return {
        roundNo: rn,
        base: c.base,
        bonus: c.bonus,
        penalty: c.penalty,
        total: c.total,
        complete: c.complete,
        time: Number(recs[rn].time) || 0,
      };
    });
    item.teams.push({
      no: t.no,
      name: t.name,
      group: t.group,
      school: t.school,
      coach: t.coach,
      members: t.members,
      status: t.status,
      voided: !!S.dqCases.find((d) => d.teamId === t.id && d.status === '已生效'),
      note: t.note,
      records: order.map((rn) => Object.assign({}, recs[rn])),
      expectPerRound: perRound,
      // expect = 取优那一轮的结果，与榜单所用口径一致
      // （总分 ↓ → 用时 ↑ → 轮次小者；与前端 bestRoundOf / 后端 engine.BestOf 同规则）
      expect: bestOf(perRound),
    });
  });

  api.standings(scn.id).forEach((r) => {
    item.standings.push({
      rank: r.rank,
      no: r.team.no,
      name: r.team.name,
      group: r.team.group,
      base: r.base,
      bonus: r.bonus,
      penalty: r.penalty,
      total: r.total,
      time: r.time,
      complete: r.complete,
      dq: !!r.dq,
      rounds: r.rounds || [],
      bestRound: r.bestRound || 0,
      award: r.award || '',
      tie: !!r.tie,
    });
  });

  golden.events.push(item);
});

/**
 * 取优那一轮：总分 ↓ → 用时 ↑ → 轮次小者。
 *
 * 注意这里**不复用前端的 bestRoundOf**：它读的是 S.scores 的原始结构，
 * 而本函数手上只有「已算好的逐轮结果」。自己再实现一遍是**有意**的 ——
 * 若前端把取优规则改错（例如漏掉"轮次小者优先"），这里仍按正确规则挑，
 * 于是 expect 与 standings 对不上，测试立刻红。
 * 反过来，若这里跟前端一起错，测试就永远绿 —— 那才是真正危险的。
 */
function bestOf(perRound) {
  if (!perRound.length) return { base: 0, bonus: 0, penalty: 0, total: 0, complete: false };
  let best = perRound[0];
  for (let i = 1; i < perRound.length; i++) {
    const cur = perRound[i];
    const better =
      cur.total > best.total ||
      (cur.total === best.total && cur.time < best.time) ||
      (cur.total === best.total && cur.time === best.time && cur.roundNo < best.roundNo);
    if (better) best = cur;
  }
  return best;
}

const maskApi = makeApi({ events: [], teams: [], scores: {} });
['张一', '程俊淇', '张', '', '欧阳娜娜', 'A', '李', '  ', '张三丰'].forEach((s) => {
  golden.maskPersonCases.push({ in: s, out: maskApi.maskPerson(s) });
});

[
  '张一 / 李二',
  '韩五、杨六、朱七',
  '王小明,李小红，张小三|赵小四',
  '程俊淇',
  '',
  '   ',
  '周吴郑 / ',
].forEach((s) => {
  golden.maskCases.push({ in: s, out: maskApi.maskMembers(s) });
});

// ===========================================================================
// 3. 覆盖度自检（缺一类分支就报错，避免"基准悄悄变瘦"）
// ===========================================================================

const coverage = {
  per_card: (e) => e.event.penaltyRule && e.event.penaltyRule.template === 'per_card',
  record_only: (e) => e.event.penaltyRule && e.event.penaltyRule.template === 'record_only',
  two_rounds: (e) => e.teams.some((t) => t.records.length === 2),
  single_round: (e) => e.teams.some((t) => t.records.length === 1),
  no_record: (e) => e.teams.some((t) => t.records.length === 0),
  voided: (e) => e.teams.some((t) => t.voided),
  dq: (e) => e.standings.some((s) => s.dq),
  withdrawn: (e) => e.teams.some((t) => t.status === 'withdrawn'),
  incomplete: (e) => e.teams.some((t) => t.expectPerRound.some((x) => !x.complete)),
  bonus_positive: (e) => e.teams.some((t) => t.expectPerRound.some((x) => x.bonus > 0)),
  bonus_zero: (e) => e.teams.some((t) => t.expectPerRound.some((x) => x.bonus === 0)),
  penalty_positive: (e) => e.teams.some((t) => t.expectPerRound.some((x) => x.penalty > 0)),
  penalty_zero: (e) => e.teams.some((t) => t.expectPerRound.some((x) => x.penalty === 0)),
  tie: (e) => e.standings.some((s) => s.tie),
  // 递补两档各要有场景，且**都留着作废队** ——
  // 没有作废队时两档结果完全一样，那种对照等于没测。
  substitute_none_with_void: (e) =>
    e.substituteMode === 'none' && e.teams.some((t) => t.voided),
  substitute_rank_with_void: (e) =>
    e.substituteMode === 'rank' && e.teams.some((t) => t.voided),
  dq_rank_zero: (e) => e.standings.some((s) => s.dq && s.rank === 0),
};

const missing = Object.keys(coverage).filter(
  (k) => !golden.events.some((e) => coverage[k](e))
);
if (missing.length) {
  console.error('❌ 基准覆盖度不足，缺少这些分支的用例：' + missing.join(', '));
  process.exit(1);
}

// ===========================================================================
// 4. 落盘 + 自检输出
// ===========================================================================

fs.mkdirSync(path.dirname(outPath), { recursive: true });
fs.writeFileSync(outPath, JSON.stringify(golden, null, 2) + '\n', 'utf8');

console.log('基准已生成:', outPath);
console.log('  来源:', path.basename(demoPath));
console.log('  REF_TIME:', golden.refTime);
console.log('  源指纹: sha256 ' + golden.sourceInfo.sha256.slice(0, 16) + '… / ' + golden.sourceInfo.bytes + ' 字节');
console.log(
  '  依赖闭包: ' + needFns.size + ' 个函数 / ' + varDecls.length + ' 个常量（自动展开，无需手写清单）'
);
if (FN_DUPES.length) console.log('  ⚠ 重复定义（后者覆盖前者）: ' + FN_DUPES.join(', '));

let teamTotal = 0;
let caseTotal = 0;
let ties = 0;
let awardNonEmpty = 0;
golden.events.forEach(function (e) {
  teamTotal += e.teams.length;
  e.teams.forEach((t) => {
    caseTotal += t.expectPerRound.length;
  });
  e.standings.forEach((s) => {
    if (s.award) awardNonEmpty++;
  });
  for (let i = 1; i < e.standings.length; i++) {
    const prev = e.standings[i - 1];
    const cur = e.standings[i];
    if (prev.total === cur.total && prev.time === cur.time) ties++;
  }
  console.log(
    '  ' +
      e.id.padEnd(28) +
      e.teams.length +
      ' 队 / 逐轮 ' +
      e.teams.reduce((n, t) => n + t.expectPerRound.length, 0) +
      ' 例 / 榜 ' +
      e.standings.length +
      ' 行' +
      (e.frontendValidation.errs.length ? ' / ⚠ 前端校验 error ' + e.frontendValidation.errs.length + ' 条' : '') +
      (e.frontendValidation.warns.length ? ' / warn ' + e.frontendValidation.warns.length + ' 条' : '')
  );
});
console.log('  合计：' + teamTotal + ' 支队伍 / ' + caseTotal + ' 个逐轮计分用例');
console.log('  递补口径: 主场景 = ' + SUBSTITUTE_DEFAULT +
  '（产品默认：名次保留空缺）；另有 substitute_rank 场景覆盖「按名次顺延」');

if (EXCLUDED.length) {
  console.log('  ⚠ 未纳入对照（不是漏了，是刻意排除，原因见下）：');
  EXCLUDED.forEach((x) => {
    console.log('    · ' + x.what + '\n      ' + x.why);
  });
}

if (awardNonEmpty === 0) {
  console.log(
    '  ⚠ 前端基准里**没有一行奖项非空** —— award 断言语义上仍是"比两边都是空串"。\n' +
      '    原因：demo 已无奖项档位配置（TIERS / rankRule.awardTiers 均已移除），\n' +
      '    后端也只在配置了 awardTiers 时才分配。奖项分配的对照因此是**空断言**，\n' +
      '    需要靠 engine 自身的单测守护，不能指望这份基准。'
  );
}
if (ties > 0) {
  console.log(
    '  ⚠ 存在 total 与 time 同时相同的相邻名次 ' + ties + ' 处（本轮为**有意构造**的并列用例）。\n' +
      '    此时排序落到「队名」兜底比较器：前端 localeCompare(zh) 拼音序，\n' +
      '    Go 用 x/text/collate + language.Chinese，已对齐（TestParityWithFrontendStandings 守护）；\n' +
      '    但两端排序表来源不同（浏览器 ICU vs x/text），生僻字可能仍有差异。'
  );
}
