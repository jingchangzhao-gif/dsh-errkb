# 干净虚拟机验收（EKB-052）

[English](acceptance-vm.md)

> **状态：阻塞，等待复测。** 验收脚本、安装器源码和开发机演练已经存在，但还没有在干净 Windows、标准用户、真实重启环境中完成整套验收。在证据目录和全部通过结果出现之前，EKB-052、EKB-056 与最终发布门槛都不能标记完成。

> **产物警告（2026-10-22）。** EKB-056 本地候选为 `errkb.exe` `B116E5BA…`、安装器 `F3E77D4C…`。`dist\release\errkb-0.1.0\` 中的旧文件来自其他修订，不在本轮演练覆盖范围内。Web 工具箱与启动器也不在这份证据中；它们需要完成 EKB-057～060 后重新验收。

## 验收目的

这项验收必须证明最终用户不需要 Go、Node.js、CUE 或仓库源码即可完成：

```text
安装 → 首次启动 → 记录错误 → 写入解法 → 再次命中
     → 重启 Windows → 自动恢复 → 升级/卸载
```

任何“在开发机上能运行”的结果都不能替代干净机证据。

## 被测产物

验收时只能使用同一次构建产生的文件：

| 产物 | 要求 |
| --- | --- |
| `errkb-<version>-setup.exe` | 版本、commit 与 manifest 一致 |
| `errkb.exe` | 与安装器内部 EXE 哈希相同 |
| `SHA256SUMS` | 覆盖安装器、EXE、工具箱 ZIP 与生成文档 |
| `manifest.json` | 记录 tag、commit、构建时间、工具链和每个产物哈希 |
| `errkb-toolbox.zip` | EKB-060 完成后加入；只包含白名单文件 |

验收前必须从空的 `dist/release/<version>/` 重新生成，禁止复用旧发布目录。

## 必需环境

- Windows 10/11 x64 干净快照。
- 标准用户账号；只在安装器明确要求时接受 UAC。
- 机器上没有 Go、Node.js、pnpm、CUE 或仓库 checkout。
- 已安装 PostgreSQL 16 服务端程序，但没有 errkb 专用实例。
- 端口 5433 空闲。
- 可以创建安装前、安装后和重启后的快照。

## 运行方式

在仓库开发机上先验证产物；把安装包与验收脚本复制进干净 VM 后，按阶段运行：

```powershell
# 校验产物
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase artifacts `
  -ExpectedSha256 <installer-sha256>

# 干净机预检；正式证据不得使用 AllowAdmin/AllowDirtyHost
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase preflight

# 交互安装后执行
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase install -Port 5433
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase firstrun -Port 5433
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase loop -Port 5433

# 准备标记，然后从开始菜单真实重启 Windows
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase rebootprepare -Port 5433
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase afterreboot -Port 5433

# 分别验证保留数据与删除数据
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase uninstall -KeepData -Port 5433
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase uninstall -Port 5433
```

## 必须人工确认的内容

- 安装器向标准用户正确显示 UAC 与 PostgreSQL 三选一页面。
- 默认专用实例使用 5433，不改动机器原有的 PostgreSQL。
- 安装结束时没有错误弹窗或隐藏失败。
- 开始菜单快捷方式能打开工具箱，且不会出现命令行窗口。
- 搜索、详情/复制、统计/审计和设置/删除预览四类页面都可用。
- 近似结果有明显警告，可信度不可编辑，删除必须二次确认。
- 重启后不手工启动任何东西，数据库与 daemon 自动恢复。
- 卸载时“保留数据”和“删除数据”两种选择都符合说明。

## 自动断言

| # | 检查 | 通过条件 |
| --- | --- | --- |
| 1 | 产物一致性 | 版本、commit、EXE 哈希、manifest 与校验和一致 |
| 2 | 首次安装 | 安装退出 0，安装目录和受管 PostgreSQL 出现 |
| 3 | Schema | `db status` 为 0008/0008，重复迁移无副作用 |
| 4 | 后台服务 | Named Pipe 可连接，实时 health 为 ok |
| 5 | 首次错误 | `errkb run` 记录一条失败 |
| 6 | 解法复用 | 写入解法后，第二次相同失败返回该解法且不调用模型 |
| 7 | 反馈 | success/failure/unknown 能记录并按规则影响可信度 |
| 8 | 重启恢复 | 真实重启后 5433 与 Named Pipe 在时限内恢复 |
| 9 | 保留卸载 | EXE/启动项删除，数据目录保留并可被重装复用 |
| 10 | 删除卸载 | 进程、端口、启动项、安装目录和数据全部清理 |
| 11 | UI 安全 | 只监听回环地址、token 必需、页面无外部资源 |
| 12 | 隐私 | 证据、日志和发布包不包含密码、token 或用户绝对路径 |

## 证据目录

每次正式验收必须新建目录：

```text
<temp>\errkb-vm-acceptance-<timestamp>\
  results.json
  environment.json
  artifacts.sha256.txt
  install.exit.txt
  db-status.json
  daemon-health.json
  first-run.txt
  second-run.txt
  after-reboot.txt
  ui-smoke.txt
  uninstall-keep.txt
  uninstall-delete.txt
```

每条检查都必须写明 expected、actual、ok、是否人工、证据文件。缺少证据等于未通过。

## 当前已经证明的内容

- 开发机上可以创建 PostgreSQL 16 专用实例。
- 当前源码的 `errkb db migrate` 可以应用 `0001`–`0008`，重复运行保持幂等。
- 当前安装器源码会在启动 daemon 前调用迁移。
- HKCU Run 脚本可以启动集群与 daemon。
- 通用 CLI 可以完成“记录失败 → 写入解法 → 再次返回解法 → 记录反馈”的闭环。
- 保留数据和删除数据两条卸载路径在开发机上做过演练。

这些结果只证明源码路径可行，不证明现有发布目录可用。

## 当前仍未证明的内容

- 干净标准用户机器上的完整交互安装。
- 从真实 Windows 重启恢复，而不是手工重放 Run 命令。
- 当前源码、安装器、发布 ZIP 和安装后 EXE 属于同一个 tag/commit。
- 开始菜单打开 Web 工具箱。
- EKB-058/059 的详情、编辑、统计、审计和设置页面。
- 工具箱关闭、daemon 崩溃或 PostgreSQL 停止后的可靠恢复。
- 公开发布流程和代码签名。

## 通过条件

只有以下条件同时满足，才能把本文件状态改为“通过”：

1. 干净 VM 证据目录存在，所有自动断言为 `ok: true`。
2. 所有人工门槛有日期、系统版本、操作者和结果记录。
3. EKB-057～060 已完成，并包含 UI 冒烟证据。
4. `pnpm`、Go、CUE、数据库集成、隐私和格式门槛全部通过。
5. README、项目状态、安装说明和中英文实施计划与实际产物一致。

