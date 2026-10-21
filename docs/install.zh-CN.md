# 安装候选、升级与卸载

[English](install.md)

本文明确区分三类内容：**当前安装器源码**、**本地构建的验收候选**和**旧发布目录**。它们不能混用。

> **状态说明（2026-10-22）。** EKB-056 已在当前源码中加入 `errkb db migrate`、安装阶段迁移和 HKCU 登录启动；这些能力在开发机上完成过演练，但干净 VM 仍未验收。`dist\release\errkb-0.1.0\` 中的文件早于 EKB-056，不能被当成当前安装器。项目目前没有受支持的公开版本。详情见 [`project-status.zh-CN.md`](project-status.zh-CN.md) 与 [`acceptance-vm.md`](acceptance-vm.md)。

## 系统要求

- Windows 10 或 11，x64。
- PostgreSQL 16。安装器可以管理专用实例，但不会把 PostgreSQL 服务端打进 EXE。
- 约 50 MB 应用空间，另加 PostgreSQL 数据目录。
- 最终运行不需要 Go、Node.js 或 CUE；从源码构建时才需要开发工具链。

## 产物及其含义

| 产物 | 来源 | 当前可用性 |
| --- | --- | --- |
| `dist\errkb.exe` | `packaging\windows\build.ps1` | 本地验收候选；不包含 Web 工具箱启动路由 |
| `dist\errkb-<version>-setup.exe` | 当前安装器源码 | 本地验收候选；未通过干净 VM |
| `dist\release\errkb-0.1.0\` | 旧发布打包结果 | 已过期；其中 EXE 没有 `db` 命令 |
| 未来的 `errkb-toolbox.zip` | EKB-060 | 尚未生成 |

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

当前安装器**不会**创建工具箱启动器或开始菜单快捷方式。直接双击 `errkb.exe` 只会打印 CLI 用法并退出，不会打开工具箱。该缺口属于 EKB-060。

## 安装目录

| 路径 | 内容 |
| --- | --- |
| `%LOCALAPPDATA%\errkb\` | EXE、生命周期脚本、迁移、README、卸载器 |
| `%LOCALAPPDATA%\errkb\pg\data\` | 专用 PostgreSQL 数据目录 |
| `%LOCALAPPDATA%\errkb\pg\credentials.json` | 端口、数据库名、用户、生成的密码和数据目录；仅所有者可读 |
| `%LOCALAPPDATA%\errkb\logs\` | PostgreSQL 与 daemon 日志 |
| `%LOCALAPPDATA%\errkb\health.json` | daemon 最近一次写入的健康状态；进程停止后文件可能过期 |

## 开发环境首次运行

在正式安装包通过验收前，使用当前源码构建和显式 DSN：

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

安装器会部署 `start-errkb.cmd`，并创建运行 `errkb.exe ui --web` 的开始菜单快捷方式；不会留下可见终端。pipe 不可达时，Web 壳会读取安装目录的凭据与 migrations 来恢复 daemon。搜索、详情/编辑、统计/审计和设置页均已接入。当前剩余边界是干净 VM 验收和最终带 tag 发布，而不是 UI 功能接线。

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

两条路径都必须在干净 VM 上重新验证；现阶段只有开发机演练记录。

## 常见问题

| 现象 | 原因与处理 |
| --- | --- |
| `schema migration check failed` | schema 账本缺失或落后。使用当前源码构建运行 `errkb db migrate`；旧发布目录中的 EXE 没有 `db` 命令。 |
| `database: connection refused` | PostgreSQL 未启动或 DSN 端口错误；检查实例并运行带显式 DSN 的 `doctor`。 |
| 本地 API 不可达 | daemon 未运行或 pipe 名不一致。启动 daemon 并运行 `doctor`。 |
| `flag provided but not defined: -web` | 当前 EXE 没有 Web 壳接线；现有发布二进制无法打开工具箱。 |
| `ui --web` 等待后退出码为 4 | daemon 已停止且 UI 没有 DSN；安装凭据自动恢复尚未完成。 |
| 5433 被占用 | 选择其他端口或停止占用该端口的实例。 |
| 健康文件显示 ok，但 pipe/端口已关闭 | `health.json` 是过期快照；以实时端口、pipe 与 `doctor` 结果为准。 |

## 相关文档

- [`project-status.zh-CN.md`](project-status.zh-CN.md)：当前源码与产物事实、发布阻塞项。
- [`acceptance-vm.md`](acceptance-vm.md)：干净 VM 验收脚本与证据。
- [`toolbox-ui.zh-CN.md`](toolbox-ui.zh-CN.md)：UI 状态和 EKB-057～060 范围。
- [`privacy.zh-CN.md`](privacy.zh-CN.md) / [English](privacy.md)：存储内容和隐私边界。

