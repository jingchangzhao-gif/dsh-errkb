# Windows 验收

[English](acceptance-vm.md)

本文定义阻塞发布的 Windows 自动检查，以及范围更小的可选人工检查。项目继续推进不再要求维护本地虚拟机。

## 验收规则

| 门禁 | 合并/发布必需 | 证明内容 |
| --- | --- | --- |
| WF-011A——无人值守生命周期 | 是 | 真实安装包可以安装应用、创建并迁移受管 PostgreSQL 集群、启动 daemon、记录并复用错误；保留数据卸载后可以重装复用，删除数据卸载后没有残留。 |
| WF-011B——确定性恢复 | 是 | 登录启动项配置正确；重复启动只保留一个 daemon；仅 daemon 故障和 daemon+数据库故障均可恢复；Named Pipe、端口、健康状态与已保存知识都会恢复。 |
| 交互式干净机检查 | 否 | 人工可额外查看安装页面、开始菜单快捷方式、SmartScreen/UAC 表现与真实 Windows 重启后的行为。 |

WF-011A 与 WF-011B 会在每个 PR 的全新、临时 GitHub 托管 Windows Runner 上通过 `windows-acceptance` 作业执行。标签发布流程会在打包或发布前重复同一套验收。人工 VM 运行可以增加信心，但不能替代或覆盖失败的自动门禁。

## 强制执行顺序

`packaging/windows/ci-acceptance.ps1` 按以下顺序执行：

1. 确认安装包存在、体积合理且 SHA-256 与本次测量值一致。
2. 确认 PostgreSQL 16 可用、5433 端口空闲且 errkb 尚未安装。
3. 静默安装真实 Inno Setup 安装包。
4. 检查安装版本、凭据 ACL、schema、健康状态、Named Pipe，并确认未监听 HTTP。
5. 记录失败命令，通过 MCP 写入解法，再次运行并获得已知解法；随后记录反馈、执行搜索并读取统计。
6. 执行 WF-011B 恢复检查。
7. 选择保留数据进行卸载。
8. 重新安装，验证保留的 connector、问题和解法，再次复用解法。
9. 选择删除数据进行卸载，并确认端口、进程、Run 启动项和应用目录全部消失。

任一自动断言失败都会以非零状态退出。只能由人确认的项目在自动证据中标为 `SKIP`，绝不会伪装为 `PASS`。

## WF-011B 恢复模型

`packaging/windows/recovery-acceptance.ps1` 不会宣称自己重启了 Windows。它会确定性地测试登录后必须恢复的产品状态：

- 检查当前用户的 `errkb-autostart` Run 启动项；
- 连续两次启动已安装产品，并确认始终只有一个已安装 daemon；
- 只终止 daemon，重放安装后的启动脚本并检查恢复；
- 同时停止 daemon 与受管 PostgreSQL 集群，重放相同启动脚本并检查恢复；
- 要求 5433 端口、Named Pipe 与最新健康文档全部恢复；
- 搜索故障前记录的问题，证明数据没有丢失。

这种方式可以在 CI 中稳定地产生失败证据。GitHub 托管 Runner 无法可靠地重启后继续同一个作业，因此真实重启保留为可选人工冒烟测试。

## CI 与发布接线

- `.github/workflows/ci.yml` 在 PR 上提供名为 `windows-acceptance` 的检查。
- `.github/workflows/release.yml` 在发布作业之前运行相同的生命周期与恢复流程。
- 只有验收作业成功后，发布作业才会收到 `WINDOWS_ACCEPTANCE_VERSION`。
- `packaging/windows/release.ps1` 会拒绝缺失或版本不一致的验收结果。
- `packaging/windows/release-test.ps1` 静态守护生命周期顺序、恢复断言与发布门禁。

仓库管理员应把 `windows-acceptance` 设为默认分支保护规则中的必需状态检查。

## 证据

PR CI 与标签流程每次都上传 `wf-011-windows-acceptance-<run-id>`。目录结构如下：

```text
windows-acceptance/
  results.json
  01-artifacts/
  02-preflight/
  03-install/
  04-first-run/
  05-error-reuse-loop/
  06-recovery/
  07-uninstall-keep/
  08-reinstall-retained/
  09-retained-first-run/
  10-retained-data/
  11-uninstall-delete/
```

每个阶段保存结构化结果以及命令、安装器、daemon、MCP 和搜索日志。证据中不得复制凭据或数据库密码。

## 本地复现

本机需要 PowerShell 7、Go、go-winres、Inno Setup 6 与 PostgreSQL 16：

```powershell
pwsh -File packaging\windows\build.ps1 -Version 0.1.0
& "<ISCC.exe>" packaging\windows\installer.iss
pwsh -File packaging\windows\ci-acceptance.ps1 `
  -ExpectedVersion 0.1.0 `
  -EvidenceDir artifacts\windows-acceptance
```

此命令会修改当前用户的 errkb 安装状态，并在结束时删除该用户的 errkb 数据。只能在一次性测试账号或临时 Runner 上执行。

## 可选人工检查

以后有合适的物理机或 Windows 11 虚拟机时，可以额外人工确认：

- 标准用户安装和全部 PostgreSQL 交互选项；
- 开始菜单启动与工具箱可见页面；
- 真实重启并登录后的行为；
- 未签名安装包的 SmartScreen/UAC 提示；
- 交互式保留/删除数据提示。

这些观察应单独记录。在项目拥有可重复的真实重启基础设施之前，它们只作为建议性证据。

## 诚实边界

自动门禁证明：产品可以在全新托管 Runner 上完成安装生命周期，并可从等价于登录启动的进程故障中恢复。它不证明真实整机重启、交互窗口的视觉质量、代码签名，也不代表所有 Windows 版本。文档和发布说明必须一直保留这一区分。
