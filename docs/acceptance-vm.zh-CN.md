# 干净机验收（历史记录）

本页仅作历史记录。安装器路线已于 2026-10-25 移除；受支持的发布形态是便携 `errkb-toolbox.zip`，真正的门禁是 CI 的 Windows 冒烟任务与 `packaging/windows/release.ps1` 的发布检查。当前状态见 [`project-status.zh-CN.md`](project-status.zh-CN.md)，安装步骤见 [`install.zh-CN.md`](install.zh-CN.md)。

早期安装器验收留下两条结论，便携形态继续沿用：

- 首次运行必须在后台服务启动前应用 schema 迁移；现在 `errkb pg install` 会应用 `migrations\`，升级由 `errkb db migrate` 覆盖。
- 登录恢复不能依赖管理员权限；现在 `autostart.ps1` 写入当前用户 HKCU Run 项。

`vm-acceptance.ps1`、`ci-acceptance.ps1` 与 `recovery-acceptance.ps1` 已随安装器删除。物理重启与交互视觉检查从未自动化；现有测试会重启后台服务与 PostgreSQL 实例，并以确定性方式重放登录命令。