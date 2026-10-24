# 安装、升级与卸载

[English](install.md)

本文明确区分正式 v0.1.1 发布文件与可丢弃的本地构建输出。

> **状态说明（2026-10-23）。** v0.1.1 是受支持的公开版本。请只从 [GitHub Release 页面](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/tag/v0.1.1)下载，并使用随附的 `SHA256SUMS` 核对文件。WF-011A/WF-011B 已在 Windows CI 中验证其生命周期与确定性恢复。详情见 [`project-status.zh-CN.md`](project-status.zh-CN.md) 与 [`acceptance-vm.zh-CN.md`](acceptance-vm.zh-CN.md)。

## 系统要求

- Windows 10 或 11，x64。
- PostgreSQL 16。安装器可以管理专用实例，但不会把 PostgreSQL 服务端打进 EXE。
- 约 50 MB 应用空间，另加 PostgreSQL 数据目录。
- 最终运行不需要 Go、Node.js 或 CUE；从源码构建时才需要开发工具链。

## 产物及其含义

| 产物 | 来源 | 当前可用性 |
| --- | --- | --- |
| `dist\errkb.exe` | `packaging\windows\build.ps1` | 当前源码构建；包含 Web 工具箱启动路由 |
| `errkb-0.1.1-setup.exe` | v0.1.1 GitHub Release | 正式安装器；WF-011A/WF-011B 在 CI 中重建并测试 |
| `dist\release\errkb-0.1.0\` | 旧发布打包结果 | 已过期；其中 EXE 没有 `db` 命令 |
| `errkb-toolbox.zip` | v0.1.1 GitHub Release | 已核对的便携工具箱 |

不要混用 `dist\` 与 `dist\release\errkb-0.1.0\` 中的文件。正式发布必须从同一个带 tag 的 Git 提交一次性重建，并让 EXE、安装器、manifest、校验和与安装后文件都指向同一修订。

当前产物没有代码签名，Windows SmartScreen 可能告警。安装前应检查 `SHA256SUMS`。

## 当前安装器源码会做什么

1. 按用户安装到 `%LOCALAPPDATA%\errkb`。
2. 让用户选择创建专用 PostgreSQL、复用已有实例或改用其他端口。
3. 创建分支默认使用 5433，不触碰已有的 5432 实例。
4. 启动专用实例。
5. 调用当前 EXE 的 `db migrate`，应用 `0001`–`0008`。
6. 通过 HKCU Run 注册当前用户登录启动，并启动 daemon。
7. 安装卸载器；卸载时允许保留或删除数据。

当前安装器会部署 `start-errkb.cmd`，并创建隐藏终端启动 `errkb.exe ui --web` 的开始菜单快捷方式。直接双击 `errkb.exe` 仍只打印 CLI 用法；界面入口是快捷方式或明确的 `ui --web` 命令。

## 安装目录

| 路径 | 内容 |
| --- | --- |
| `%LOCALAPPDATA%\errkb\` | EXE、生命周期脚本、迁移、README、卸载器 |
| `%LOCALAPPDATA%\errkb\pg\data\` | 专用 PostgreSQL 数据目录 |
| `%LOCALAPPDATA%\errkb\pg\credentials.json` | 端口、数据库名、用户、生成的密码和数据目录；仅所有者可读 |
| `%LOCALAPPDATA%\errkb\logs\` | PostgreSQL 与 daemon 日志 |
| `%LOCALAPPDATA%\errkb\health.json` | daemon 最近一次写入的健康状态；进程停止后文件可能过期 |

## 安装后首次运行

安装目录会把 `pg\credentials.json` 与 `migrations\` 放在 `errkb.exe` 旁边，
下列命令无需任何参数：

```powershell
errkb db status
errkb doctor
errkb daemon
errkb ui --web
```

## 开发环境首次运行

源码开发或诊断仍可使用显式 DSN：

```powershell
go build -o errkb.exe ./cmd/errkb
.\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe doctor --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
```

不要把包含密码的 DSN 写进 Git、日志或截图。

## 工具箱 UI

当前源码构建提供：

```powershell
.\errkb.exe ui --web
```

安装器会部署 `start-errkb.cmd`，并创建运行 `errkb.exe ui --web` 的开始菜单快捷方式；不会留下可见终端。pipe 不可达时，Web 壳会读取安装目录的凭据与 migrations 来恢复 daemon。搜索、详情/编辑、统计/审计和设置页均已接入，并随 v0.1.1 正式发布。

## 注册到 LLM 宿主

`errkb mcp` 走 stdio 的 MCP 协议，会自己解析安装目录的 `pg\credentials.json` 与 `migrations\`，宿主只需启动这个二进制：

```powershell
errkb mcp install --host codex     # codex mcp add errkb -- <路径>\errkb.exe mcp
errkb mcp install --host claude    # claude mcp add --scope user ... errkb -- <路径>\errkb.exe mcp
errkb mcp install --host dsh       # 打印 dsh-errkb 插件路径（不需要 MCP 条目）
```

任意宿主加 `--print` 只显示命令、不执行。注册后重启宿主；`errkb mcp selftest` 在内存里验证 `initialize` 与 `tools/list`，`errkb doctor` 运行同一项检查。

## 复用已有 PostgreSQL

把配置中的 `database.dsn` 或 daemon 的 `--dsn` 指向已有实例，创建目标数据库，运行 `errkb db migrate`，再运行 `errkb doctor`。安装器的专用实例选项只负责受管路径。

## 备份

知识库是普通 PostgreSQL 数据库，可以使用 `pg_dump`：

```powershell
$cred = Get-Content "$env:LOCALAPPDATA\errkb\pg\credentials.json" | ConvertFrom-Json
$env:PGPASSWORD = $cred.password
$pgDump = Join-Path ${env:ProgramFiles} "PostgreSQL\16\bin\pg_dump.exe"
& $pgDump -Fc -h 127.0.0.1 -p $cred.port -U $cred.superuser -d $cred.database -f errkb.dump
```

恢复时应使用新的空数据库。`internal/database` 还提供带 SHA-256 manifest、表计数和约束检查的脚本化备份/恢复能力。

## 升级

- 应用升级：未来通过新版安装器覆盖同一目录，保留数据。
- Schema 升级：只通过迁移器应用；已应用迁移不能被修改，失败事务必须回滚。
- PostgreSQL 主版本升级：当前没有完成自动化；升级前必须备份。
- 工具箱升级：必须确认启动器、EXE、Web 资源和发布 manifest 属于同一版本。

## 卸载

- **保留数据：** 删除应用文件和启动项，保留 `pg\` 数据以供重装复用。
- **删除数据：** 停止 daemon 与集群，删除数据、凭据和安装目录。

静默卸载无法回答对话框：`unins000.exe /VERYSILENT` 保留数据，`unins000.exe /VERYSILENT /DELETE_DATA=1` 删除数据。第一阶段的卸载器把选择写成标记文件，第二阶段和 `postgres.ps1` 读取该标记，因此两个阶段不会给出相反的结果。

WF-011A 会在临时 Windows Runner 上验证两条路径：保留数据卸载后重装并核对原有知识，随后删除数据卸载并检查进程、端口、启动项和文件残留。

## 常见问题

| 现象 | 原因与处理 |
| --- | --- |
| `schema migration check failed` | schema 账本缺失或落后。当前安装会从 `pg\credentials.json` 自动解析连接，运行 `errkb db status` 与 `errkb db migrate`；早期发布二进制没有 `db` 命令。 |
| `database: connection refused` | PostgreSQL 未启动或 DSN 端口错误；检查实例并运行带显式 DSN 的 `doctor`。 |
| 本地 API 不可达 | daemon 未运行或 pipe 名不一致。启动 daemon 并运行 `doctor`。 |
| `ui --web` 等待后退出码为 4 | 无法根据安装凭据恢复 daemon；检查 `pg\credentials.json`、受管集群和 daemon 日志，开发环境可显式传入 DSN。 |
| 5433 被占用 | 选择其他端口或停止占用该端口的实例。 |
| 健康文件显示 ok，但 pipe/端口已关闭 | `health.json` 是过期快照；以实时端口、pipe 与 `doctor` 结果为准。 |

## 相关文档

- [`project-status.zh-CN.md`](project-status.zh-CN.md)：当前源码与产物事实、发布阻塞项。
- [`acceptance-vm.zh-CN.md`](acceptance-vm.zh-CN.md)：强制 Windows CI 验收、证据与可选人工检查。
- [`toolbox-ui.zh-CN.md`](toolbox-ui.zh-CN.md)：UI 状态和 EKB-057～060 范围。
- [`privacy.zh-CN.md`](privacy.zh-CN.md) / [English](privacy.md)：存储内容和隐私边界。

