# 通用 Web 页面框架说明

> 适用范围：Go 后端 + 静态单页（SPA）+ JSON API 的管理控制台类网页。本框架脱胎于一个管理面板项目的通用结构，已剔除全部项目专有内容（品牌、业务字段、接口、路由、配置项、数据），可直接作为生成网页框架的指导输入。示例一律为占位符（`{{field}}` / `示例值`）。

---

## 决策定稿

| 决策项 | 定稿 |
|---|---|
| 渲染模式 | **模式 B**：静态 SPA + JSON API |
| 前端技术栈 | **原生 JS，零依赖、零构建**；页面骨架 HTML + 独立 JS/CSS 文件，无内联脚本，严格 CSP |
| 数据访问层 | 先定义存储接口（`internal/store`），默认内存态先行，接口化以便替换为 DB / 外部服务 |
| 鉴权 | **Bearer api_key**（可为空 = 不鉴权），常量时间比较 |
| 页面规模 | 较小，**不需要真正多页路由**，单页内按视图切换 |
| 主题 | **需要明暗切换**（跟随系统 → 浅 → 深 三态） |
| 部署形态 | **单二进制部署**（静态资源 `go:embed` 打进二进制） |
| Go 版本 | 默认 **Go 1.22+** 方法路由（兼容方案见 §6.2） |
| 移动端权重 | 偶尔访问：只做**基础响应式**，不做深度适配 |
| 实时性 | **轮询即可**，不上 WebSocket/SSE |

---

## 1. 页面定位与布局骨架

### 1.1 通用页面类型归类
本文架适用**管理控制台 / 运维面板 / 数据看板**类页面，典型构成：

| 类型 | 特征 | 适用场景 |
|---|---|---|
| 仪表盘 | 统计卡片 + 核心表格 + 关键提醒 | 资源池、订单、设备、任务调度等状态总览 |
| 明细列表 | 筛选栏 + 分页数据表 | 日志、流水、历史记录 |
| 配置页 | 分组表单 + 校验 + 保存/重置 | 服务配置、策略参数、定时任务 |
| 弹窗交互 | 模态框承载新建/编辑/导入/确认 | 新增对象、导入、二次确认、密钥录入 |
| 实时日志流 | 频道过滤 + 自动滚动 | 运行日志、事件流 |

### 1.2 整体布局
```
┌─────────┬──────────────────────────────────┐
│ 侧边导航  │  顶栏（视图标题 / 面包屑 / 全局操作）      │
│         ├──────────────────────────────────┤
│ Brand    │  主内容区（同一时刻仅一个视图，前端切换）      │
│ 导航项 1  │  ┌─ 区块1：统计卡 / 关键提醒 ────────┐  │
│ 导航项 2  │  ├─ 区块2：筛选栏 + 数据表 ─────────┤  │
│ ...      │  └─ 区块3：补充明细 / 图表 ────────┘  │
│ 状态页脚  │                                    │
└─────────┴──────────────────────────────────┘
```
- 左侧固定宽导航（如 208px）：品牌区 / 菜单 / 底部状态页脚（运行状态、版本、后端模式指示灯）。
- 顶栏：标题 + 副标题（层级）、右侧全局动作（主题切换、刷新、新建等主操作）。
- 主内容区：用 `<section data-view>` + `hidden` 切换，hash 驱动，前进/后退可恢复。
- 模态层：遮罩 + 居中弹窗；消息层：Toast 容器收口所有轻提示。

### 1.3 容器、栅格与响应式断点策略
- 桌面（主场景）：`grid-template-columns: 导航宽 1fr` 双栏；主区容器自适应 + 内边距。
- 统计卡栅格：`repeat(auto-fit, minmax(列宽, 1fr))` 自动换行。
- 断点：桌面默认 >760px；**≤760px（基础级）** 汉堡按钮 + 抽屉导航 + 遮罩、顶栏文字按钮收敛、宽表 `overflow-x: auto` 横向滚动。**不做**宽表卡片化等深度适配（偶尔移动访问）。
- `prefers-reduced-motion: reduce` 时关闭全部动画/过渡。

## 2. 信息架构与区块划分

| 区块 | 通用作用 | 是否可复用 | 备注 |
|---|---|---|---|
| 品牌区 | 展示产品名/版本 | ✅ | 内容替换即可 |
| 导航菜单 | 视图入口列表，含当前态 | ✅ | 菜单项即视图清单 |
| 状态页脚 | 运行状态、版本、后端模式指示灯 | ✅ | 数据来自概览接口 |
| 顶栏操作区 | 主题切换、刷新、新建等全局动作 | ✅ | 主题切换为通用亮点，建议保留 |
| 统计卡片区 | 数字化呈现核心指标 | ✅ | 卡片数与语义色（ok/warn/bad）可配置 |
| 关键提醒卡 | 展示需关注的到期/异常条目 | ✅ | 可折叠，随接口结果出现/隐藏 |
| 筛选栏 | 搜索 + 多维度下拉 + 排序 + 重置 | ✅ | 通用度最高，建议标准化 |
| 数据表区 | 数据列 / 状态列 / 操作列 | ✅ | 列头可配置，状态列加语义标签 |
| 详情区 | 与当前视图相关的补充明细/图表 | ✅ | 按视图承载，非必选 |
| 配置表单区 | 分组标题 + 字段 + 保存/重置 | ✅ | "服务/任务/策略/高级/日志"分组法通用 |
| 日志查看区 | 实时日志 + 频道过滤 + 自动滚动 | ✅ | 运维类页面实用 |
| 历史记录区 | 变更/事件留痕列表 | ✅ | 只读展示 |
| 模态框 | 新建/编辑/导入/二次确认/密钥输入 | ✅ | 统一遮罩 + 弹窗 + 底部操作条 |
| Toast 通知 | 操作结果轻提示 | ✅ | 统一容器 |

## 3. 通用组件清单

| 组件 | 通用职责 | 关键属性/插槽 | 状态 |
|---|---|---|---|
| `sidebar-nav` | 视图入口与当前高亮 | `items[]`(label/icon/hash)、当前项 | 普通 / 当前(on) |
| `topbar` | 标题、面包屑、全局按钮区 | 标题、副标题、`actions[]` | — |
| `stat-card` | 显示单一指标 | `value`、`label`、语义色 | 正常 / 空值(占位符 `-`) |
| `alert-card` | 需关注的提醒条目 | 标题、说明、折叠、操作 | 隐藏 / 展开 / 无数据 |
| `filter-bar` | 复合查询 | `search`、`select[]`、`sort`、`reset`、结果数 | 默认 / 已筛选 |
| `data-table` | 数据渲染；渲染后校验每列数据与列标题一一对应 | `columns[]`(数据/状态/操作列)、`rows` | 加载中 / 空 / 有数据 |
| `status-tag` | 语义状态 | 文本、色(ok/warn/bad/neutral) | 各语义态 |
| `paginator` | 分页（必须） | 当前页、页大小、总数；携带 `limit/offset` | 一律服务端分页、小批量加载，避免一次渲染全量数据 |
| `form-section` | 配置分组 | 标题、说明、`fields[]`、`save/reset` | 可含"改动需重启生效"提示 |
| `form-field` | 输入项 | label、`input\|select\|checkbox\|password`、hint、校验 | 正常 / 错误 / 只读 / 禁用 |
| `password-reveal` | 密码明文切换 | 绑定输入框 | 隐藏 / 明文 |
| `log-viewer` | 实时日志流 | 频道 chips、自动滚动开关、行数上限 | 空 / 自动滚动开/关 |
| `modal` | 弹窗容器 | 标题、说明、body、底部 `actions[]` | 隐藏 / 显示(overlay+on) |
| `tab-panel` | 弹窗内多方案（如"登录/导入"） | `tabs[]`、`panels[]` | 各 Tab 切换 |
| `toast` | 轻提示 | 文案、类型(success/error/info) | 自动消失 / 可关闭 |
| `key-modal` | 鉴权失败时录入凭证 | 输入框 + 错误提示 + 确认 | 隐藏 / 错误 |
| `theme-toggle` | 明暗切换 | 跟随系统 → 浅 → 深 循环 | auto / light / dark |

## 4. 样式规范

### 4.1 颜色变量建议（CSS 变量，双主题）
"冷基础色板 + 单一强调色 + 语义状态色"；**语义色只用于状态，强调色只用于交互**。

```css
:root[data-theme="dark"] {
  --bg: #0c0e14;            /* 页面底色 */
  --surface: #14171f;       /* 卡片表面 */
  --surface-2: #1a1e28;     /* 次级面：hover/表头 */
  --raise: #202531;         /* 凸起面 */
  --line: #262c3a;          /* 主分隔线 */
  --ink: #e8ebf2;           /* 主文字 */
  --ink-2: #a8b0c2;         /* 次级文字 */
  --ink-3: #6b7488;         /* 弱化/占位文字 */
  --accent: #5b7cfa;        /* 强调色（可替换） */
  --accent-ink: #fff;
  --accent-soft: rgba(91,124,250,.13);
  --ok: #3ddc97;  --warn: #f5b544;  --bad: #f0655f;   /* 语义色 */
  --shadow: 0 1px 2px rgba(0,0,0,.4), 0 8px 24px -12px rgba(0,0,0,.5);
}
:root[data-theme="light"] { /* 反转明暗并降低对比 */ }
```

### 4.2 字体层级
| 层级 | 建议规格 |
|---|---|
| 页面标题 | 15–16px 中等字重，`letter-spacing:-0.01em` |
| 区块标题 | 13.5–14px 加粗 |
| 正文/表格 | 13–14px，`line-height:1.5–1.55` |
| 辅助说明 | 11.5–12px，`--ink-3` |
| 数字/指标 | 等宽字体（`tabular-nums`）保证对齐 |
| 中文字体栈 | `system-ui + PingFang SC + Microsoft YaHei` 兜底 |

### 4.3 间距 / 圆角 / 边框 / 阴影
- 间距：4px 递进，常用 `4/8/10/12/16/20/24`；卡片内边距 16–20px；区块间距 16px。
- 圆角：导航项/按钮/输入框 6–8px；卡片 8–12px。
- 边框：`--line` 1px；hover 用 `--surface-2` 背景而非加深边框。
- 阴影：仅卡片/弹窗/浮层；焦点态用 `accent` 2px outline + 2px offset。

### 4.4 按钮 / 表单 / 表格规则
- 按钮：主（accent 底白字）、次（透明/描边）、危险（bad 底）、表内小尺寸 `xs`、图标 ghost。
- 表单：label + 输入/选择/密码 + hint；"改动需重启"独立标记；select 与输入框等高。
- 表格：表头 `--surface-2`，行 hover，斑马纹可选；数值列右对齐（等宽数字）；操作列固定最右，用 `xs` 按钮。

### 4.5 响应式规则（基础级）
- `≤760px`：导航抽屉（汉堡 + 遮罩 + `aria-expanded`）、顶栏文字按钮收敛/抽屉化、宽表仅横滚。
- `min-width:0` 防溢出；`overflow-x:auto` 包表。
- 关闭 `prefers-reduced-motion` 动画。

## 5. 交互与状态设计

| 状态 | 通用做法 |
|---|---|
| 加载态 | 初载占位符（`-`/骨架）；弹窗内"加载中（dots）"；表格空时显示"加载中" |
| 空状态 | 图标 + 主文案 + 副说明（引导首个动作），不空白 |
| 错误态 | 失败 Toast + 保留旧数据；解析类错误显示错误块并可重试；各接口失败互不阻塞 |
| 成功提示 | Toast（可含关键结果值）；批量操作给"成功数/失败数"汇总 |
| 鉴权失败 | 统一 401 拦截 → 弹凭证录入框 → 成功后重发；凭证存 `localStorage`（键名带版本号） |
| 禁用/只读 | 无前置条件时按钮禁用 + title 提示；配置保存区分"立即生效/需重启字段" |
| 确认 | 危险/不可逆操作用确认；幂等重复操作不加确认 |
| 防重复提交 | 后端按对象加锁（重复返回"执行中"），前端点击后即时禁用 |
| 轮询 | 异步流程（登录回调、批量触发、指标刷新）前端 3–5s 轮询，`done:false` 继续 |
| 筛选/排序 | 状态集中管理、搜索防抖、可重置、结果计数 |
| 分页 | 数据一律分页展示（服务端 `limit/offset`）；默认页大小按数据体积设定，前端只渲染当前页，翻页/筛选重新请求，避免浏览器一次渲染过多记录 |
| 列对齐校验 | 每次渲染表格后校验"每一列数据与其列标题一一对应"（列新增/删除/字段调整后必须复核），防止表头与下方记录错位 |
| 数据刷新 | 手动"刷新"按钮；关键指标按需轮询；后台刷新标注"x 分钟前更新" |
| 自动滚动 | 日志流提供开关（默认开）并持久化 |
| 主题 | 三态（跟随系统→浅→深），存 `localStorage`，只改 `<html data-theme>` |
| 视图切换 | hash 驱动、刷新/回退可恢复；各视图筛选状态保留在 JS 状态对象 |
| 折叠 | 统一 `aria-expanded` 并持久化折叠偏好 |

## 6. Go Web 实现映射

### 6.1 总体原则（模式 B 定稿）
- 页面骨架 `index.html` + 前端逻辑 `app.js` + 样式 `app.css`；**CSP 下页面零内联脚本**，脚本独立文件。
- 全部静态资源 `go:embed` 进二进制 → **单文件部署**。
- 页面自身可匿名加载（无秘密）；**敏感数据只走 API，由 `withAuth` 统一鉴权**。

### 6.2 路由组织建议（Go 1.22+ 方法路由）
```go
mux := http.NewServeMux()
mux.HandleFunc("GET /{$}", pageIndex)         // 页面骨架
mux.HandleFunc("GET /assets/app.js", pageScript)
mux.HandleFunc("GET /assets/app.css", pageCSS)
mux.HandleFunc("GET /api/overview", withAuth(handleOverview))
mux.HandleFunc("GET /api/items", withAuth(handleList))
mux.HandleFunc("POST /api/items/{id}/action", withAuth(handleAction))
log.Fatal(http.ListenAndServe(":"+port, withMiddleware(mux)))
```
- 页面路由与 `/api/*` 分组；API 统一 `withAuth`。
- 兼容：若 Go < 1.22，改用 `chi` 或 `httprouter` 等第三方路由。
- 安全头中间件包裹全部响应（页面 + API + 401 错误）。

### 6.3 中间件链建议
| 中间件 | 职责 | 典型实现 |
|---|---|---|
| 安全头 | CSP / `X-Content-Type-Options` / `X-Frame-Options` / `Referrer-Policy` / COOP / CORP | 包装 `ServeHTTP` |
| 恢复 | `recover()` → 500 JSON + 日志 | 防单 panic 拖垮进程 |
| 请求日志 | method/path/status/耗时/客户端 IP | 标准库 log 即可 |
| 静态文件 | `go:embed` 直出 | 无缓存或短缓存 |
| 鉴权 | 仅包 API，页面放行 | Bearer + 常量时间比较，api_key 空 = 放行 |

### 6.4 页面渲染 vs JSON API 取舍（结论）
- 定稿：**JSON API + 前端渲染**。
- API 响应统一信封：`{ "ok": bool, "data": …, "error": … }`；空列表回 `[]` 而非 `null`；列表接口一律支持分页（`limit/offset`），响应含总条数（`total`）；参数统一命名。
- 前端统一 `api(path)` 封装：注凭证头、解析信封、401 弹凭证框。

### 6.5 通用目录树示例（单二进制 + go:embed）
```
project/
├── cmd/
│   └── server/
│       ├── main.go            # 装配依赖 + 路由
│       └── wiring.go          # 依赖注入（存储、缓存、调度器…）
├── internal/
│   ├── web/                   # 前端资源（全部 go:embed）
│   │   ├── index.html         # 页面骨架（含视图容器/弹窗/Toast）
│   │   ├── app.js             # 前端逻辑入口
│   │   ├── app.css            # 样式（含设计变量）
│   │   └── mobile.css         # 基础响应式覆盖（可选合并）
│   ├── handler/               # HTTP handler（overview/list/action…）
│   ├── api/                   # JSON 信封、参数解析、校验
│   ├── middleware/            # 安全头 / 恢复 / 日志 / 鉴权
│   ├── store/                 # 数据访问接口（内存 / DB / 外部服务可换）
│   └── config/                # 配置加载 + 在线修改 + 需重启字段标注
├── scripts/                   # 辅助脚本
└── go.mod
```

## 7. 可复用模板骨架

### 7.1 页面骨架（index.html）
```html
<!DOCTYPE html>
<html lang="zh-CN" data-theme="dark">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{AppName}} · {{ViewTitle}}</title>
  <link rel="stylesheet" href="/assets/app.css">
</head>
<body>
<div class="shell">
  <nav class="sidebar">
    <div class="brand"><div class="name">{{AppName}}</div><div class="sub">{{AppSub}}</div></div>
    <ul class="nav-list">
      <li><a href="#{{viewKey}}" data-view="{{viewKey}}">{{ViewLabel}}</a></li>
      <!-- 其余视图项… -->
    </ul>
    <div class="status"><span class="pulse"></span><span data-nav-state>{{Connecting}}</span></div>
  </nav>

  <div class="main">
    <div class="topbar">
      <button class="icon nav-btn" data-navbtn aria-expanded="false" aria-label="{{OpenNav}}">
        <svg aria-hidden="true">…</svg></button>
      <h2 data-ttl>{{ViewTitle}}</h2>
      <span class="sep">/</span><span class="meta" data-submeta>-</span>
      <span class="grow"></span>
      <button class="icon" data-theme-toggle title="{{ToggleTheme}}">…</button>
      <button data-refresh>{{Refresh}}</button>
      <button class="primary" data-create>{{Create}}</button>
    </div>

    <section class="view" data-view="{{viewKey}}">
      <!-- 区块：统计卡 / 提醒卡 / 筛选+表格 / 详情…见 7.2 -->
    </section>
    <!-- 其余视图 section[data-view][hidden]… -->
  </div>
</div>

<div class="veil" data-modal></div><!-- 各模态框 -->
<div id="toasts"></div>
<script src="/assets/app.js"></script>
</body>
</html>
```

### 7.2 页面内容模板（区块骨架，全部占位符）
```html
<!-- 统计卡片区 -->
<section class="stat-row">
  <div class="stat-card {{Tone}}"><div class="v">{{Value}}</div><div class="k">{{Label}}</div></div>
</section>

<!-- 提醒卡（可折叠） -->
<aside class="alert-card" data-foldable>
  <header><h3>{{AlertTitle}}</h3><span class="grow"></span>
    <button class="xs" data-fold aria-expanded="true">{{Collapse}}</button></header>
  <div class="pad" data-fold-body>…</div>
</aside>

<!-- 筛选栏 + 数据表 -->
<section class="card">
  <header><h3>{{TableTitle}}</h3><span class="grow"></span>
    {{Actions}}…</header>
  <div class="filter-bar">
    <input type="search" placeholder="{{SearchPlaceholder}}" data-filter="q">
    {{Filters}}<select data-filter="{{Key}}">…</select>
    <button class="ghost" data-reset>{{Reset}}</button>
    <span class="note" data-count></span>
  </div>
  <div class="table-wrap">
    <table class="data-table">
      <thead><tr>{{Columns}}<th class="col-actions"></th></tr></thead>
      <tbody data-table-body></tbody>
    </table>
  </div>
</section>
```

### 7.3 通用组件模板
```html
<!-- 模态框 -->
<div class="veil" data-modal="{{Id}}">
  <div class="dialog" style="width:{{Width}}">
    <header><h3>{{Title}}</h3><div class="hint">{{Hint}}</div></header>
    <div class="body">{{Body}}</div>
    <footer><span class="grow"></span>
      {{Actions}}<button class="{{Class}}" data-act="{{Key}}">{{Label}}</button></footer>
  </div>
</div>

<!-- Tab 弹窗 -->
<div class="tabs">
  <button class="tab on" data-tab="a">{{TabA}}</button>
  <button class="tab" data-tab="b">{{TabB}}</button>
</div>
<div class="tab-panel" data-panel="a">{{PanelA}}</div>
<div class="tab-panel" data-panel="b" hidden>{{PanelB}}</div>

<!-- 空状态 -->
<div class="empty">
  <svg aria-hidden="true">{{Icon}}</svg>
  <div class="t">{{EmptyTitle}}</div><div class="d">{{EmptyDesc}}</div>
</div>
```

### 7.4 样式变量（app.css 顶部）
```css
:root[data-theme="dark"] { /* §4.1 变量块 */ }
[data-theme="light"] { /* 反转 */ }
*{box-sizing:border-box;margin:0;padding:0}
[hidden]{display:none!important}
body{background:var(--bg);color:var(--ink);font:14px/1.55 var(--sans)}
:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:4px}
@media (prefers-reduced-motion:reduce){*{transition:none!important;animation:none!important}}
/* 语义化组件类：.sidebar/.topbar/.card/.stat-row/.filter-bar/.data-table/.veil/.dialog/.toast/.tab */
```

### 7.5 脚本入口（app.js 骨架）
```js
'use strict';
/* 状态：集中声明在顶部，避免 TDZ */
const state = { view:'{{defaultView}}', /* 各视图 filter 状态 */ };
const $ = id => document.getElementById(id);

/* 统一请求：注凭证头 / 信封解析 / 401 弹凭证框 */
async function api(path, opts={}) {
  const h = { ...(opts.headers||{}) };
  const k = localStorage.getItem('{{credKey}}');
  if (k) h['Authorization'] = 'Bearer '+k;
  if (opts.body) h['Content-Type'] = 'application/json';
  const r = await fetch('/api/'+path, { ...opts, headers:h });
  if (r.status === 401) { openCreds(); throw new Error('{{Unauthorized}}'); }
  const d = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
  return d;
}

/* 视图切换：hash 驱动 + 保留各自状态 */
function go(view){ state.view = view; /* 切 section[data-view] + 导航高亮 */ }

/* 渲染函数按视图划分：renderOverview / renderList / renderLogs / renderForm */
/* 初始化：主题、事件绑定、首屏加载、定时刷新/轮询 */
```

## 8. 验收清单

- [ ] 是否已剔除项目特有内容（品牌、业务字段、接口、路由、配置项、数据）
- [ ] 是否包含响应式规则（基础级：断点、抽屉导航、宽表横滚）
- [ ] 是否包含加载 / 空 / 错误状态
- [ ] 是否可直接映射到 Go Web（`html/template` 或 SPA+JSON，本框架 = SPA+JSON）
- [ ] 是否给出通用目录结构
- [ ] 表格数据是否已抽象为"数据列 / 状态列 / 操作列"
- [ ] 表单字段是否已抽象为"输入项 / 选择项 / 提交操作"
- [ ] 示例数据是否全部使用占位符 `{{field}}` / `示例值`
- [ ] 类名是否用语义化通用名（`data-table`、`filter-bar`…）
- [ ] 颜色是否保留为变量建议而非品牌专有名
- [ ] 是否体现定稿决策（模式 B / 原生 JS 零构建 / 单二进制 / Bearer api_key / 明暗主题 / 轮询 / 基础响应式）
- [ ] 核对表格数据中每一列展示的数据与列标题是否一致，确保表头与下方记录一一对应
- [ ] 数据一律分页展示（服务端 `limit/offset` 分页，不一次渲染全量，避免浏览器压力）
