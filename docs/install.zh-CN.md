# 安装、升级与卸载

[English](install.md)

本页介绍受支持的便携发布物：来自[最新发布页](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest)的 `errkb-toolbox.zip`。没有安装器，也没有开始菜单快捷方式；ZIP 就是发布形态。

## 系统要求

- Windows 10 或 11，x64。
- 已安装 PostgreSQL 16（EDB 安装包或已有的本地实例）。errkb 不内置数据库，但 `errkb pg install` 会用已安装的 PostgreSQL 二进制创建并管理自己的实例。请把 `pg_ctl.exe` 放进 `PATH`，或用 `ERRKB_PG_BIN` 指向 PostgreSQL 的 `bin` 目录。
- 标准（非管理员）账户即可。实例、后台服务和登录项都以当前用户身份运行，不需要提权。

## ZIP 内容

| 文件 | 用途 |
| --- | --- |
| `errkb.exe` | 单一的发布二进制（CLI、后台服务、MCP 服务、Web 工具箱宿主） |
| `start-errkb.cmd` | 打开 Web 工具箱（`errkb ui --web`），需要时自动启动后台服务 |
| `autostart.ps1` | 通过 `HKCU\...\Run` 键实现当前用户登录恢复 |
| `postgres.ps1` | `errkb pg` 的薄封装（供 `autostart.ps1` 使用） |
| `migrations\*.sql` | `errkb pg install` 应用的 schema |
| `README.md`、`CHANGELOG.md`、`LICENSE` | 文档与许可证 |

把 ZIP 解压到你有写权限的目录（例如 `%LOCALAPPDATA%\errkb-portable`）。发布物未做代码签名，请用发布页的 `SHA256SUMS` 校验 `errkb.exe`。

## 首次运行

1. 创建实例、应用 schema 并写入凭据：

   ```powershell
   .\errkb.exe pg install
   ```

   它会在 `.\pg\data` 启动一个监听 5433 端口的专用实例，应用 `migrations\`，并写入 `.\pg\credentials.json`（权限 0600）。可用参数：`--data-dir`、`--port`、`--database`、`--superuser`、`--migrations`。

   若想复用已有 PostgreSQL 服务，可跳过 `pg install`，只执行一次 `errkb db migrate --dsn <你的 DSN>`。

2. 启动产品：

   ```powershell
   .\start-errkb.cmd
   ```

   工具箱会打开本地页面；`errkb ui --web` 在后台服务未运行时自动启动，并在窗口关闭后继续运行。CLI 与 MCP 宿主使用同一个后台服务（`\\.\pipe\errkb`）。

3. 可选的登录恢复：

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File .\autostart.ps1 -Action install -AppDir "$PWD"
   ```

   它会写入当前用户的 Run 值 `errkb-autostart`。Windows 不允许标准用户注册计划任务，因此 Run 键是受支持的方式。

4. 检查状态：`.\errkb.exe pg status` 以及后台服务的 health 文件。

## 宿主接入

- **Codex / Claude Code：**`.\errkb.exe mcp install --host codex` 或 `--host claude` 会把 MCP 服务注册进宿主 CLI。DSH 使用插件，命令会打印插件方式。
- **任意 MCP 宿主：**指向 `errkb.exe mcp`。它自己解析已安装的 `pg\credentials.json`，并在管道不可达时启动后台服务。

## 升级

1. 停止后台服务，用新版本的文件替换 `errkb.exe`、`migrations\` 与脚本。
2. 应用 schema：

   ```powershell
   .\errkb.exe pg status
   .\errkb db migrate --credentials .\pg\credentials.json --migrations .\migrations
   ```

   迁移只向前。`spec/compat.cue` 的 `compat.upgrade` 记录了哪些旧 schema、旧配置与旧 Markdown 仍可加载。

## 卸载

```powershell
.\errkb.exe pg uninstall -DeleteData
powershell -NoProfile -ExecutionPolicy Bypass -File .\autostart.ps1 -Action uninstall -AppDir "$PWD"
```

省略 `-DeleteData` 可保留实例。随后删除解压目录。除了该目录与 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，不注册任何东西。

## 从源码构建

```powershell
./packaging/windows/build.ps1 -Version 0.1.2
./packaging/windows/release.ps1 -Mode package -Version 0.1.2 -Tag v0.1.2
```

`build.ps1` 生成带版本信息的二进制；`release.ps1` 生成便携 ZIP、manifest、SBOM、第三方声明、签名策略与 `SHA256SUMS`。发布检查会核对 tag、变更日志小节与二进制版本；CI 的 Windows 冒烟任务用真实 PostgreSQL 运行同一个可执行文件。

## 故障排查

| 现象 | 处理 |
| --- | --- |
| `pg install` 说找不到 PostgreSQL 二进制 | 安装 PostgreSQL 16，或把 `ERRKB_PG_BIN` 指向其 `bin` 目录 |
| `errkb db status` 说没有数据库 | 传 `--credentials .\pg\credentials.json` 或 `--dsn`；文件与 EXE 同目录时会自动解析 |
| 管道不存在 | 运行 `errkb ui --web`、`start-errkb.cmd` 或 `errkb daemon`；MCP 与工具箱会自行启动 |
| 5433 端口被占用 | `errkb pg install --port <空闲端口>`（第二个实例再加 `--data-dir`） |
| schema 版本不匹配 | `errkb db migrate --credentials .\pg\credentials.json --migrations .\migrations` |