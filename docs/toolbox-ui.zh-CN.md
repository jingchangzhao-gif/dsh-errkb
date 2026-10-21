# 工具箱 UI 设计与交付状态（本地 Web 壳）

[English](toolbox-ui.md)

状态：**已在本地分支实现，尚未评审、合入或发布。** 工作副本包含 EKB-057 的搜索壳、EKB-058 的详情与编辑页面、EKB-059 的统计、审计与设置页面，统一由 `errkb.exe ui --web` 提供；`packaging/windows/release.ps1` 会产出 `errkb-toolbox.zip`，安装器随包携带启动器与快捷方式。EKB-060 仍缺干净 VM 的 UI 证据与发布纪律门槛（EKB-052）。目标仍是在 `errkb.exe` 中嵌入交互式 UI，不引入 Chromium、Electron 或 NW.js。

## 当前状态

| 项目 | 状态 | 证据或缺口 |
| --- | --- | --- |
| 嵌入式 HTML/CSS/JS 与回环服务器 | 已实现 | `internal/webshell/`，受 token 保护的 HTTP 面 |
| token、CSP、回环绑定与 API | 已实现并通过测试 | 搜索、问题详情、问题/解法写入、统计、审计、设置、删除预览与确认 |
| `ui --web` 命令分派 | 已实现 | `cmd/errkb/ui.go` 把 `--web` 交给 `cmd/errkb/web.go` |
| daemon 不可达时自动启动 | 已实现 | 读取 `pg/credentials.json` 与 migrations，拉起 daemon 并等待管道 |
| 详情与编辑页面 | 已实现 | `/issues/{id}` 页面；追加版本、问题状态、解法状态与确认删除都在 daemon 事务内写审计 |
| 统计、审计与设置页面 | 已实现 | `/stats` 与 `/settings` 页面，分别消费 `/api/stats`、`/api/audit`、`/api/settings` |
| 启动器、开始菜单快捷方式与工具箱 ZIP | 本地已实现 | `start-errkb.cmd`、安装器 `[Icons]`、`release.ps1` 产出 `errkb-toolbox.zip`；待干净 VM 验收 |
| 干净 VM UI 验收 | 阻塞 | 需要 EKB-052/EKB-060 的干净 Windows 机器 |

## 架构决定

1. **UI 嵌入 `errkb.exe`。** HTML/CSS/JS 位于 `internal/webshell/assets/`，通过 `//go:embed` 编译进二进制。
2. **使用系统浏览器的应用模式作为窗口。** `errkb ui --web` 启动本机回环 HTTP 服务，优先打开 `msedge --app=<url>`，失败时回退到默认浏览器。
3. **Web 壳只负责展示。** 所有操作经 `service.Client` 和 Named Pipe 访问 daemon；Web 层不实现检索、可信度、脱敏或存储逻辑，也不直连 PostgreSQL。
4. **只监听回环地址，并使用每次启动独立的 token。** 服务绑定 `127.0.0.1` 临时端口；所有 `/api/*` 请求必须带 `X-Errkb-Token`；响应使用严格 CSP，不加载外部资源。

## 目标进程模型

```text
start-errkb.cmd / 开始菜单快捷方式
  -> errkb.exe ui --web
       -> 复用 daemon；不可达时使用安装凭据启动 PostgreSQL + daemon
       -> 在 127.0.0.1:<临时端口> 启动 HTTP 服务
       -> 打开 Edge 应用窗口
       -> 关闭窗口后 daemon 继续运行
```

当前缺口：Web 壳启动 daemon 时只读取 `ERRKB_DSN`/`--dsn`，还没有读取 `%LOCALAPPDATA%\errkb\pg\credentials.json`；因此上面的“使用安装凭据恢复”尚未实现。

## 页面范围

| 页面 | 内容 | 状态 |
| --- | --- | --- |
| 搜索 | 地址栏、前 3 条结果、精确/近似徽标、匹配原因、空态 | EKB-057 本地部分完成 |
| 问题详情 | 摘要、出现记录、环境、解法版本、结果、只读可信度、复制解法 | EKB-058 未开始 |
| 统计与审计 | 假合并、命中率、p50/p95、节省估算、审计过滤 | EKB-059 未开始 |
| 连接器与隐私 | 连接器开关、采集暂停、保留期、HTTP 状态、删除预览与确认 | EKB-059 未开始 |

近似结果必须明确标记；可信度只能读取；删除必须先预览再确认；任何写操作都必须写审计记录。

## HTTP 接口

| 路由 | 状态 | 所属任务 |
| --- | --- | --- |
| `GET /`、`GET /assets/*` | 本地已实现 | EKB-057 |
| `POST /api/search` | 本地已实现 | EKB-057 |
| 问题详情和解法修改路由 | 计划中 | EKB-058 |
| 统计、审计、设置和删除路由 | 计划中 | EKB-059 |

所有接口都必须复用现有契约字段，校验请求，返回稳定错误码，并禁止返回原始 DSN、密码或未脱敏样本。

## 打包与发布

以下内容是 **EKB-060 的目标**，不是当前行为：

- `packaging/windows/build.ps1` 把静态资源嵌入 EXE。
- `release.ps1` 按严格白名单生成 `errkb-toolbox.zip`。
- `installer.iss` 安装启动器，并创建运行 `errkb.exe ui --web` 的开始菜单快捷方式；登录时不自动打开 UI。
- ZIP、安装器和安装后的 EXE 必须标识同一个 tag/commit，并进入同一组发布校验和。

当前现实：没有工具箱 ZIP；`start-errkb.cmd` 没有进入发布包；安装器没有 `[Icons]`；已有二进制拒绝 `ui --web`。

## 测试要求

- 单元测试：token 拒绝、CSP、静态资源、路由校验、只绑定回环地址。
- 集成测试：启动 `ui --web --no-browser`，经真实 daemon 完成搜索、详情、统计和删除预览/取消。
- 安装冒烟：从开始菜单打开、四个页面可访问、无外部资源、窗口关闭后 daemon 保留。
- 干净 VM 人工证据：标准用户安装、首次打开、真实重启、保留数据卸载与删除数据卸载。

## 任务顺序

1. **EKB-057：本地已实现。** 服务器、嵌入资源、token、搜索页、详情页导航、凭据感知的 daemon 启动、e2e 与发布脚本中的工具箱 ZIP 都在；待评审与合入。
2. **EKB-058：本地已实现。** 详情读取、追加版本、问题状态与解法状态写入、需确认的删除；每次编辑都在 daemon 事务内留下审计行，e2e 覆盖写入路径。
3. **EKB-059：本地已实现。** 实测统计、可过滤的审计页，以及带删除预览与取消的设置页；e2e 验证预览不写数据。
4. **EKB-060：部分实现。** 启动器、快捷方式与工具箱 ZIP 已有；干净 VM 的 UI 证据与发布纪律门槛仍缺。

