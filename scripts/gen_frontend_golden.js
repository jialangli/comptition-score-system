#!/usr/bin/env node
/**
 * 生成「前端对照基准」（P2 验收工具）
 *
 * 背景：后端 engine 包是按前端原型 `赛事统分后台管理_demo.html` 里的
 * computeTotal / standings / maskPerson / maskMembers / validateEvent
 * 逐一移植的。移植是否忠实，不能靠肉眼看代码，必须拿同一份输入跑出结果来比。
 *
 * 做法：从 demo HTML 里**原样抽取**前端实现（不是复制粘贴一份到本文件，
 * 否则前端改了这里不会跟着变），用同一份种子数据算出期望值，
 * 输出 testdata/frontend_golden.json；再由 Go 单测读这份基准做逐位断言。
 *
 * 这样一来，前端一旦改了算法，重跑本脚本 + go test 就能立刻发现两端分叉。
 *
 * 用法：
 *   node scripts/gen_frontend_golden.js [demo.html 路径] [输出路径]
 */
'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_DEMO = 'D:/Desktop/workbuddy/赛事统分后台管理_demo.html';

const demoPath = process.argv[2] || DEFAULT_DEMO;
const outPath =
  process.argv[3] || path.join(__dirname, '..', 'testdata', 'frontend_golden.json');

// ---------------------------------------------------------------------------
// 1. 抽取前端的声明
// ---------------------------------------------------------------------------

/** 带字符串/注释感知的花括号配平，按名字截取一个完整的 function 声明。 */
function sliceFunction(src, name) {
  const marker = 'function ' + name + '(';
  const start = src.indexOf(marker);
  if (start < 0) throw new Error('未找到函数声明: ' + name);
  const braceStart = src.indexOf('{', start);
  if (braceStart < 0) throw new Error('函数体缺失: ' + name);

  let depth = 0;
  let inStr = null;
  let escaped = false;
  let inLine = false;
  let inBlock = false;

  for (let i = braceStart; i < src.length; i++) {
    const c = src[i];
    const n = src[i + 1];

    if (inLine) {
      if (c === '\n') inLine = false;
      continue;
    }
    if (inBlock) {
      if (c === '*' && n === '/') {
        inBlock = false;
        i++;
      }
      continue;
    }
    if (inStr) {
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === inStr) inStr = null;
      continue;
    }
    if (c === '/' && n === '/') {
      inLine = true;
      i++;
      continue;
    }
    if (c === '/' && n === '*') {
      inBlock = true;
      i++;
      continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      inStr = c;
      continue;
    }
    if (c === '{') depth++;
    else if (c === '}') {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error('花括号未配平: ' + name);
}

/** 抽取一个顶层 var 赋值语句（只支持到分号，够用且不会误吞）。 */
function sliceVar(src, name) {
  const re = new RegExp('var\\s+' + name + '\\s*=\\s*');
  const m = re.exec(src);
  if (!m) throw new Error('未找到变量声明: ' + name);
  const eq = m.index + m[0].length;
  const open = src.indexOf('{', eq);
  const semicolon = src.indexOf(';', eq);
  if (open >= 0 && open < semicolon) {
    // 对象字面量：配平到对应的右花括号，再吃掉分号
    let depth = 0;
    for (let i = open; i < src.length; i++) {
      if (src[i] === '{') depth++;
      else if (src[i] === '}') {
        depth--;
        if (depth === 0) {
          const end = src.indexOf(';', i);
          return src.slice(m.index, (end < 0 ? i + 1 : end + 1));
        }
      }
    }
    throw new Error('变量花括号未配平: ' + name);
  }
  if (semicolon < 0) throw new Error('变量未以分号结束: ' + name);
  return src.slice(m.index, semicolon + 1);
}

const html = fs.readFileSync(demoPath, 'utf8');

const FN_NAMES = [
  'clone',
  'seedData',
  'taskRawScore',
  'computeTotal',
  'standings',
  'maskPerson',
  'maskMembers',
  'validateEvent',
];

const pieces = [sliceVar(html, 'REF_TIME'), sliceVar(html, 'TIERS')];
for (const name of FN_NAMES) pieces.push(sliceFunction(html, name));

// 抽取的关键：在同一个函数体里 eval，各声明互相可见；
// 结束时把需要的内容 return 出来，避免污染全局。
const body =
  pieces.join('\n\n') +
  '\n\nvar S = seedData();\n\nreturn { REF_TIME: REF_TIME, TIERS: TIERS, S: S,' +
  ' computeTotal: computeTotal, standings: standings,' +
  ' maskPerson: maskPerson, maskMembers: maskMembers, validateEvent: validateEvent };\n';

const F = new Function(body)();

// ---------------------------------------------------------------------------
// 2. 用种子数据算出期望值
// ---------------------------------------------------------------------------

const golden = {
  source: path.basename(demoPath),
  note: '由 scripts/gen_frontend_golden.js 从 demo 前端实现自动生成，请勿手工编辑',
  generatedAt: new Date().toISOString(),
  refTime: F.REF_TIME,
  events: [],
  maskPersonCases: [],
  maskCases: [],
};

F.S.events.forEach(function (ev) {
  const item = { id: ev.id, event: ev, teams: [], standings: [], frontendValidation: F.validateEvent(ev) };

  F.S.teams
    .filter(function (t) {
      return t.eventId === ev.id;
    })
    .forEach(function (t) {
      const r = F.S.scores[t.id] || {};
      const c = F.computeTotal(ev, r);
      item.teams.push({
        no: t.no,
        name: t.name,
        group: t.group,
        school: t.school || '',
        coach: t.coach || '',
        members: t.members || '',
        status: t.status || 'active',
        record: {
          roundNo: 1,
          tasks: r.tasks || {},
          time: Number(r.time) || 0,
          yellow: Number(r.yellow) || 0,
          red: Number(r.red) || 0,
          signed: !!r.signed,
        },
        expect: {
          base: c.base,
          bonus: c.bonus,
          penalty: c.penalty,
          total: c.total,
          complete: c.complete,
        },
      });
    });

  F.standings(ev.id).forEach(function (r) {
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
      award: r.award || '',
      tie: !!r.tie,
    });
  });

  golden.events.push(item);
});

['张一', '程俊淇', '张', '', '欧阳娜娜', 'A', '李', '  ', '张三丰'].forEach(function (s) {
  golden.maskPersonCases.push({ in: s, out: F.maskPerson(s) });
});

[
  '张一 / 李二',
  '韩五、杨六、朱七',
  '王小明,李小红，张小三|赵小四',
  '程俊淇',
  '',
  '   ',
  '周吴郑 / ',
].forEach(function (s) {
  golden.maskCases.push({ in: s, out: F.maskMembers(s) });
});

// ---------------------------------------------------------------------------
// 3. 落盘 + 自检输出
// ---------------------------------------------------------------------------

fs.mkdirSync(path.dirname(outPath), { recursive: true });
fs.writeFileSync(outPath, JSON.stringify(golden, null, 2) + '\n', 'utf8');

console.log('基准已生成:', outPath);
console.log('  来源:', path.basename(demoPath));
console.log('  REF_TIME:', golden.refTime);
let teams = 0;
let ties = 0;
golden.events.forEach(function (e) {
  teams += e.teams.length;
  for (let i = 1; i < e.standings.length; i++) {
    const prev = e.standings[i - 1];
    const cur = e.standings[i];
    if (prev.total === cur.total && prev.time === cur.time) ties++;
  }
  console.log(
    '  ' +
      e.id +
      ': ' +
      e.teams.length +
      ' 队 / 榜单 ' +
      e.standings.length +
      ' 行 / 前端校验 ' +
      (e.frontendValidation.errs.length
        ? 'error ' + e.frontendValidation.errs.length + ' 条'
        : '无 error') +
      (e.frontendValidation.warns.length ? ' / warn ' + e.frontendValidation.warns.length + ' 条' : '')
  );
});
console.log('  合计队伍:', teams);
if (ties > 0) {
  console.log(
    '  ⚠ 存在 total 与 time 同时相同的相邻名次 ' + ties + ' 处 —— ' +
      '此时排序会落到「队名」兜底比较器（两端结果是否一致取决于它）。\n' +
      '    前端：localeCompare(zh) 拼音序；Go：x/text/collate + language.Chinese 拼音序，\n' +
      '    已对齐（TestParityWithFrontendStandings 守护）。但两端排序表来源不同\n' +
      '    （浏览器 ICU vs x/text），生僻姓氏可能仍有差异 —— 加了生僻字队伍后务必重跑测试。'
  );
} else {
  console.log('  ✅ 无 total+time 双同分相邻名次，兜底比较器不会被触发（两端排序结果一致）');
}
