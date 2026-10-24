# 工具箱 UI 设计与交付状态（本地 Web 壳）

[English](toolbox-ui.md)

状态：**已实现、有自动化测试覆盖，并随 v0.1.2 发布。** 当前源码包含 EKB-057 的搜索壳、EKB-058 的详情与编辑页面、EKB-059 的统计、审计与设置页面，统一由 `errkb.exe ui --web` 提供；`errkb-toolbox.zip` 携带启动器与登录脚本（安装器路线已移除）。交互界面视觉检查是可选证据。目标仍是在 `errkb.exe` 中嵌入交互式 UI，不引入 Chromium、Electron 或 NW.js。

## 当前状态

| 项目 | 状态 | 证据或缺口 |
| --- | --- | --- |
| 嵌入式 HTML/CSS/JS 与回环服务器 | 已实现 | `internal/webshell/`，受 token 保护的 HTTP 面 |
| token、CSP、回环绑定与 API | 已实现并通过测试 | 搜索、问题详情、问题/解法写入、统计、审计、设置、删除预览与确认 |
| `ui --web` 命令分派 | 已实现 | `cmd/errkb/ui.go` 把 `--web` 交给 `cmd/errkb/web.go` |
| daemon 不可达时自动启动 | 已实现 | 读取 `pg/credentials.json` 与 migrations，拉起 daemon 并等待管道 |
| 详情与编辑页面 | 已实现 | `/issues/{id}` 页面；追加版本、问题状态、解法状态与确认删除都在 daemon 事务内写审计 |
| 统计、审计与设置页面 | 已实现 | `/stats` 与 `/settings` 页面，分别消费 `/api/stats`、`/api/audit`、`/api/settings` |
| 启动器与工具箱 ZIP | 已实现 | `start-errkb.cmd`、`autostart.ps1`、`release.ps1` 产出 `errkb-toolbox.zip` |
| 交互界面视觉检查 | 可选 | 可由人工在干净机查看 Edge 应用模式、复制行为、SmartScreen/UAC 与真实重启 |

## 架构决定

1. **UI 嵌入 `errkb.exe`。** HTML/CSS/JS 位于 `internal/webshell/assets/`，通过 `//go:embed` 编译进二进制。
2. **使用系统浏览器的应用模式作为窗口。** `errkb ui --web` 启动本机回环 HTTP 服务，优先打开 `msedge --app=<url>`，失败时回退到默认浏览器。
3. **Web 壳只负责展示。** 所有操作经 `service.Client` 和 Named Pipe 访问 daemon；Web 层不实现检索、可信度、脱敏或存储逻辑，也不直连 PostgreSQL。
4. **只监听回环地址，并使用每次启动独立的 token。** 服务绑定 `127.0.0.1` 临时端口；所有 `/api/*` 请求必须带 `X-Errkb-Token`；响应使用严格 CSP，不加载外部资源。

## 目标进程模型

```text
start-errkb.cmd
  -> errkb.exe ui --web
       -> 复用 daemon；不可达时使用安装凭据启动 PostgreSQL + daemon
       -> 在 127.0.0.1:<临时端口> 启动 HTTP 服务
       -> 打开 Edge 应用窗口
       -> 关闭窗口后 daemon 继续运行
```

Windows 冒烟任务覆盖构建与生命周期基础；`release.ps1` 从已验收 tag 生成并核对最终发布包。真实重启和界面观感仅作可选人工证据。

## 页面范围

| 页面 | 内容 | 状态 |
| --- | --- | --- |
| 搜索 | 地址栏、前 3 条结果、精确/近似徽标、匹配原因、空态 | EKB-057 本地已实现 |
| 问题详情 | 摘要、出现记录、环境、解法版本、结果、只读可信度、复制解法 | EKB-058 本地已实现 |
| 统计与审计 | 假合并、命中率、p50/p95、节省估算、审计过滤 | EKB-059 本地已实现 |
| 连接器与隐私 | 连接器开关、采集暂停、保留期、HTTP 状态、删除预览与确认 | EKB-059 本地已实现 |

近似结果必须明确标记；可信度只能读取；删除必须先预览再确认；任何写操作都必须写审计记录。

## HTTP 接口

| 路由 | 状态 | 所属任务 |
| --- | --- | --- |
| `GET /`、`GET /assets/*` | 本地已实现 | EKB-057 |
| `POST /api/search` | 本地已实现 | EKB-057 |
| `GET /api/issues/{id}` 与问题/解法修改路由 | 本地已实现 | EKB-058 |
| `GET /api/stats`、`GET /api/audit`、设置与删除路由 | 本地已实现 | EKB-059 |

所有接口都必须复用现有契约字段，校验请求，返回稳定错误码，并禁止返回原始 DSN、密码或未脱敏样本。

## 打包与发布

EKB-060 的打包实现当前已经做到：

- `packaging/windows/build.ps1` 把静态资源嵌入 EXE。
- `release.ps1` 按严格白名单生成 `errkb-toolbox.zip`。
- `start-errkb.cmd` 是运行 `errkb.exe ui --web` 的启动器；登录时不自动打开 UI。
- ZIP 与独立 EXE 必须标识同一个 tag/commit，并进入同一组发布校验和。

EKB-060 已被便携发布形态取代。v0.1.2 带 tag 发布包含一致的工具箱 ZIP 与独立 EXE，下载后身份和哈希已经核对。本地忽略的 `dist/` 文件不是发布权威。

## 测试要求

- 单元测试：token 拒绝、CSP、静态资源、路由校验、只绑定回环地址。
- 集成测试：启动 `ui --web --no-browser`，经真实 daemon 完成搜索、详情、统计和删除预览/取消。
- 启动冒烟：运行 start-errkb.cmd 打开、四个页面可访问、无外部资源、窗口关闭后 daemon 保留。
- 可选人工证据：首次打开、四个页面显示、复制行为、真实重启与交互提示。

## 任务顺序

1. **EKB-057：本地已实现。** 服务器、嵌入资源、token、搜索页、详情页导航、凭据感知的 daemon 启动、e2e 与发布脚本中的工具箱 ZIP 都在；待评审与合入。
2. **EKB-058：本地已实现。** 详情读取、追加版本、问题状态与解法状态写入、需确认的删除；每次编辑都在 daemon 事务内留下审计行，e2e 覆盖写入路径。
3. **EKB-059：本地已实现。** 实测统计、可过滤的审计页，以及带删除预览与取消的设置页；e2e 验证预览不写数据。
4. **EKB-060：已被便携形态取代。** v0.1.2 包含启动器、autostart 脚本与工具箱 ZIP；Windows 冒烟任务与发布检查核对最终带 tag 发布。

