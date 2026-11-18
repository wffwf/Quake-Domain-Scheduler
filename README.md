# 空探平台（Kongtan）

Quake 资产探测与日常信息收集系统。用 **Go** 实现，单二进制跨平台运行（Windows / Linux / macOS），帮助用户在每月内充分利用 360Quake 账号点数：定期对指定域名执行空间资产探测，采集 URL 与子域名，结构化存储，便于检索、下载与分析。

启动后访问 <http://127.0.0.1:8360> 使用（默认监听本机）。

---

## 功能特性

- **API 信息管理**：录入 / 编辑 / 删除 / 启用禁用 / 排序 360Quake Token；Token 脱敏展示；页面一键测试 Token 有效性；多 Token 按 `sort_order` 顺序使用，前一个不可用/额度耗尽自动切换下一个。
- **域名管理**：分组（含说明）、单条录入、批量粘贴、`txt` 文件导入；同分组内去重；按录入顺序执行。
- **黑名单**：关键词（包含匹配）/ 正则；采集入库前与 CSV 导出前**双重过滤**；匹配对象为完整 URL / host / domain。
- **探测执行引擎**：仅 `page` + `page_size` 逐页翻页（底层 `start`/`size`），**禁用滚动查询**；逐域名记录翻页过程（页码、返回条数、累计点数）；失败重试 + 多 Token 切换；点数预算耗尽即停止并标记 `partial`。
- **数据处理**：URL 解析（自动补 scheme）、注册域抽取、ICP 信息抽取、地理位置抽取、URL/子域名去重、原始 JSON 留档；**IP 绝不写入 subdomain**。
- **CSV 导出**：每次任务结束自动导出 URL / 子域名 CSV（UTF-8 with BOM，兼容 Excel 中文），也支持页面按任务下载。
- **定时调度**：每月最后 N 小时（默认 6 小时）对所有含启用域名的分组执行一次；正确处理跨月、闰月、时区；同一自然月不重复执行；时间可注入便于测试。
- **Web 界面**：仪表盘、API 管理、分组与域名、黑名单、任务管理、数据查看、配置查看，共 7 个视图；原生 JS 零构建；明暗主题三态切换；基础响应式。

---

## 技术栈

| 项 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.22+ | 使用 stdlib 方法路由（见「取舍说明」） |
| Web | `net/http` | 按《通用Web页面框架.md》定稿，静态 SPA + JSON API |
| SQLite | `modernc.org/sqlite` | 纯 Go，无需 CGO |
| 配置 | `gopkg.in/yaml.v3` | `config.yaml` |
| 定时 | `robfig/cron/v3` + 自实现月末窗口判断 | |
| CSV | `encoding/csv` | UTF-8 with BOM |
| 日志 | `log/slog` | 结构化日志 |
| 前端 | 原生 HTML/JS/CSS | `go:embed` 打进二进制，零依赖零构建 |
| 注册域 | `golang.org/x/net/publicsuffix` | 内嵌公共后缀列表，纯 Go |

---

## 目录结构

```
kongtan/
├── main.go                 # 入口：装配、启动导入、Web 服务、调度器
├── config.yaml             # 示例配置
├── go.mod / go.sum
├── internal/
│   ├── config/             # 配置加载、校验、脱敏
│   ├── db/                 # SQLite 连接、迁移、数据访问
│   ├── model/              # 数据模型与状态常量
│   ├── api/                # HTTP 路由、中间件、Handler
│   ├── quake/              # Quake API 客户端（多 Token 顺序切换 + 重试）
│   ├── probe/              # 探测任务执行引擎
│   ├── processor/          # URL/域名/ICP/位置抽取
│   ├── blacklist/          # 黑名单过滤
│   ├── scheduler/          # 月末窗口定时调度
│   ├── exporter/           # CSV 导出
│   └── sync/               # API/域名 txt 落地与启动导入
├── web/                    # 前端静态资源（index.html / app.js / app.css）
├── data/                   # SQLite 数据库、input 输入目录
└── output/                 # CSV 导出目录
```

---

## 快速开始

### 构建

```bash
cd kongtan
go build -o kongtan .
```

交叉编译（无需 CGO）：

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o kongtan_linux   .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o kongtan.exe     .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o kongtan_darwin  .
```

### 启动

```bash
./kongtan            # 默认读取当前目录 config.yaml
./kongtan /path/to/config.yaml   # 也可指定配置文件路径
```

启动后浏览器访问 <http://127.0.0.1:8360>。

> 首次启动会自动建表；若 `data/input/apis.txt` 存在且数据库为空则导入 API Token；若 `data/input/domains/<分组>.txt` 存在且对应分组为空则导入域名。

### 测试

```bash
go test ./...
```

覆盖：处理器（裸主机名解析、IP 不作为子域名、ICP/位置抽取、注册域）、调度器（月末窗口、跨月/闰年/时区）、探测引擎端到端集成（分页、黑名单、去重、CSV 导出、无滚动参数）。

---

## 配置说明（config.yaml）

```yaml
server:
  listen_ip: "127.0.0.1"       # 监听 IP
  port: 8360                    # 监听端口
  read_timeout_sec: 30
  write_timeout_sec: 60
  api_key: ""                   # 可选：非空则 /api 需要 Bearer 鉴权
  log_file: "./data/kongtan.log" # 结构化日志落盘路径（控制台仅输出关键生命周期信息）

quake:
  base_url: "https://quake.360.net/api/v3/search/quake_service"
  page_size: 1000               # 单次请求条数，≤1000
  start_page: 1                 # 起始页码（1 起）
  max_pages: 50                 # 单域名最大翻页数
  request_interval_ms: 300      # 翻页间隔
  timeout_sec: 60
  max_retries: 2
  point_budget_per_task: 50000  # 单任务点数上限（达到即停止，标记 partial）
  use_scroll: false             # 固定 false，代码中不实现 scroll 分支
  apis:                         # 静态 API（会同步导入数据库，按顺序使用）
    - { name: "primary", token: "xxxxxxxx", enabled: true }
    - { name: "backup",  token: "yyyyyyyy", enabled: true }

schedule:
  monthly_window_hours: 6
  timezone: "Asia/Shanghai"
  enabled: true

storage:
  db_path: "./data/kongtan.db"
  output_dir: "./output"
  input_dir: "./data/input"

blacklist:                     # 静态黑名单（合并进匹配器，并导入数据库）
  keywords: ["ottcn.com"]
  regexes: []
```

---

## 页面使用说明

1. **仪表盘**：当前剩余点数（多账号累加，可点「查看当前账号剩余点数」实时查询并查看分账号明细）、分组/域名/API/记录数统计、上次任务时间、最近任务列表。
2. **API 管理**：新增/编辑/删除/排序（↑↓）/启用禁用/测试 Token。Token 脱敏显示；编辑时 Token 留空表示保持不变。
3. **分组与域名**：新建/编辑/删除分组；选择分组后管理其域名；添加域名支持批量粘贴（每行一个），同分组自动去重。
4. **黑名单**：添加关键词（包含匹配）或正则；删除。命中 URL / host / domain 任一即过滤整条记录。
5. **任务管理**：任务列表（状态/耗时/记录数/点数/错误）；选择分组「立即执行」；自动/手动触发共用同一执行引擎，仅 `trigger_type` 不同。
6. **数据查看**：URL / 子域名两表切换；支持按关键词、分组、域名、host、ICP 关键字检索；展示分组、任务名、任务执行时间等列；URL 按 `url`、子域名按 `subdomain` 自动去重（保留最新记录）；**分页展示**（每页 50 条）；选择任务后下载 CSV。
7. **配置查看**：只读展示当前 `config.yaml`（敏感字段脱敏）。
8. **计划任务**：只读展示调度开关、每月最后 N 小时、时区、下一次预计执行时间、本月是否已自动执行（含任务 ID/时间）、最近一次自动任务（状态/耗时/点数）、历史自动任务列表。

---

## 数据与 txt 同步

- API Token 每次增删改/排序后，同步写入 `data/input/apis.txt`（每行 `name,token,enabled,sort_order`）。
- 域名每次增删改后，同步写入 `data/input/domains/<分组名>.txt`（每行一个域名），并清理已删除分组的孤儿文件。
- 启动时若数据库为空（API）或分组为空（域名），自动从上述 txt 导入；若 `apis.txt` 缺失/为空，则回退从 `config.yaml` 的 `quake.apis` 导入。

---

## 关键取舍说明（PR 记录）

1. **Web 框架**：规格建议 `gin`，但《通用Web页面框架.md》定稿为「Go 1.22+ stdlib 方法路由 + 静态 SPA + JSON API」。按文档要求「冲突以框架文档为准」，故采用 `net/http` 方法路由，未引入 gin。
2. **分页参数**：规格使用 `page`/`page_size`，参考样例 `quake_probe.py` 的实际线协议为 `POST` JSON body `{"query","start","size"}`。实现上配置保留 `page_size`/`start_page`，内部映射为 `start=(page-1)*page_size`、`size=page_size`，逐页 `start += page_size`。**不调用滚动查询接口，请求中不携带 `scroll_id`/`scroll`/`cursor`，代码无 scroll 分支**（`use_scroll` 恒为 `false`）。
3. **点数统计**：参考样例未定义响应中的点数字段，故默认**每条返回记录计 1 点**作为累计消耗（`consumed_points`）；若响应 `meta.consumed_points` 存在则优先采用。仪表盘改为展示「当前剩余点数」（来自 `user/info` 接口，多账号累加），任务级 `consumed_points` 仍用于任务列表与月度统计。
4. **注册域抽取**：引入 `golang.org/x/net/publicsuffix`（内嵌 PSL，纯 Go 无 CGO），`wap.js.10086.cn → 10086.cn` 等复杂后缀（`*.com.cn`/`*.co.uk`）也能正确处理；失败回退「最后两级标签」。
5. **tasks 表扩展**：规格未列 `task_name` 列，但 URL/子域名记录需携带 `task_name`，故在 `tasks` 表冗余存储该列，便于任务↔记录关联与导出。
6. **黑名单双来源**：`config.yaml` 静态关键词/正则与数据库（页面维护）合并生效；首次启动数据库黑名单为空时从配置导入，使页面可见。

---

## 关键实现细节

- **请求构造**（严格遵循 `quake_probe.py`）：`POST {base_url}`，头 `Content-Type: application/json` + `X-QuakeToken: <token>`，体 `{"query","start","size"}`；响应信封 `{code, message, data[]}`，`code==0` 成功；401 响应体为 `/quake/login` 时判定鉴权失败。
- **URL 解析**：取 `service.http.http_load_url[0]`，兜底 `service.http.load_url`；无 scheme 自动补 `http://`；`Hostname()` 去端口；host 为 IP 时绝不写 `subdomain`，仅跳过子域名记录（URL 记录的 `ip` 仍保留）。
- **兜底策略**：URL 解析不出 host 时仅回退 `hostname` / `domain` 字段，**绝不回退到 `ip`**。
- **ICP/位置**：ICP 位于 `service.http.icp`（实测真实线协议，兜底顶层 `icp`）：`service.http.icp.licence`、`service.http.icp.main_licence.unit/nature`、`service.http.icp.is_expired`；位置取 `location.gps`（Quake 为 `[经度, 纬度]`，故 `gps_lat=gps[1]`、`gps_lng=gps[0]`）及 `location` 下的省市/ISP/AS/场景（缺失留空）。
- **去重**：入库时 URL 表按 `url`（同任务内）、子域名表按 `(subdomain, domain)`（同任务内）；数据查看页再按 `url` / `subdomain` 全局去重（保留最新记录），并展示任务执行时间（联表读取 `tasks.started_at`）。
- **剩余点数**：`GET https://quake.360.net/api/v3/user/info`（头 `X-QuakeToken`）返回 `data.credit`（剩余总积分）与 `data.month_remaining_credit`（本月剩余）。仪表盘按启用账号累加展示，`POST /api/credits/refresh` 实时查询并缓存；启动时后台首次查询一次。
- **日志落盘**：结构化日志（含 HTTP 访问日志、Quake 请求告警等）统一写入 `server.log_file`（默认 `./data/kongtan.log`，追加写）；控制台仅输出关键生命周期信息（加载配置、导入条数、启动/停止等），避免访问记录刷屏。
- **配置模板自动释放**：启动时检测当前工作目录 `config.yaml`，缺失则写出内嵌默认模板并提示绝对路径后退出（不覆盖已存在文件）；已存在则提示「已加载配置文件」。仅针对默认 `config.yaml`，显式指定其他路径时不触发。
- **调度自测**：`POST /api/scheduler/selftest` 为纯判定（不请求 Quake、不写任务记录）；内置 8 组用例（月中/窗口起点前一秒/窗口起点/月末最后一秒/同月第二次/跨月首次/闰年 2 月/时区切换上海·UTC），时间点相对 `monthly_window_hours` 计算，表驱动单测覆盖（页面入口已隐藏，仅保留后端与单测）。
- **并发控制**：同分组防重复提交（后端加锁），前端执行按钮即时反馈。

---

## 验收对照

| 验收项 | 状态 |
|---|---|
| 三平台 `go build`（无 CGO） | ✅ 已交叉编译验证 |
| 访问 `http://127.0.0.1:8360` | ✅ |
| 导入 2 Token + 分组域名后「立即执行」 | ✅（集成测试覆盖） |
| 无滚动查询（仅 start/size） | ✅（集成测试断言无 scroll 参数） |
| `wap.js.10086.cn` → subdomain / `10086.cn`，无 IP 子域名 | ✅（单测 + 集成测试） |
| 黑名单 `ottcn.com` 生效 | ✅（集成测试断言结果不含该关键词） |
| `point_budget_per_task` 生效标记 partial | ✅（引擎逻辑，`accumulator.consumed >= budget` 即停） |
| 测试模式可注入时间触发月末窗口 | ✅（`Scheduler.SetNow` + 纯函数单测） |
| CSV 中文不乱码、字段顺序一致 | ✅（UTF-8 BOM + 表头顺序断言） |
| 计划任务页面只读展示调度信息与历史自动任务 | ✅（`GET /api/schedule`，无编辑入口） |
| 调度自测 8 用例 + `go test ./internal/scheduler/` 通过、不请求 Quake | ✅（`Judge` 纯判定 + 表驱动单测） |
| 配置模板自动释放（删除 config.yaml → 重新生成并打印路径；再次启动不覆盖；只读目录报错退出） | ✅（`config.EnsureFile` + 启动分支） |
