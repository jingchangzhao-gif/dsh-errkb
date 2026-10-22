# 测试记录

[English](test-record.md)

本文长期记录完整测试门禁在哪个环境、使用哪些命令、得到什么结果，避免仅为保存“曾经通过”这一事实而让工作流条目一直保持开放。单项证据仍写入实施计划、CHANGELOG 与验收文档。

## 2026-10-22——开发机，WF-010

环境：Windows 11 Pro for Workstations 10.0.26200、Go 1.26.4 windows/amd64、Node v24.16.0、pnpm 11.7.0，以及 `127.0.0.1:5432` 上的 PostgreSQL 16.15 外部开发实例。数据库测试通过 `ERRKB_TEST_DATABASE_URL` 使用临时 schema。

执行命令：

```powershell
pnpm.cmd typecheck
pnpm.cmd lint
pnpm.cmd test
pnpm.cmd format:check
gofmt -l cmd internal tests
go vet ./...
cue vet ./spec/...
$env:ERRKB_TEST_DATABASE_URL = "<已脱敏测试 DSN>"
go test ./... -count=1
```

结果：

- `pnpm typecheck` 无错误；`pnpm lint` 为 10 条已知警告、0 错误；`pnpm test` 共 16 个文件、733 项测试通过，覆盖率达到门槛；`pnpm format:check` 通过。
- `gofmt -l` 无输出；`go vet ./...` 与 `cue vet ./spec/...` 通过。
- `go test ./... -count=1` 所有 package 通过。部分耗时：cmd/errkb 22.1 秒、internal/database 63.6 秒、internal/service 43.9 秒、internal/capture 8.4 秒、internal/audit 2.6 秒、internal/webshell 0.1 秒、tests/e2e 11.5 秒。
- e2e 覆盖 CLI 闭环、工具箱详情/编辑、统计/设置、`errkb tail --file` 与 Markdown 导入/导出往返。

说明与边界：

- `TestTrustIsDatabaseOnly` 现在只统计自身 schema 中的 `errkb_trust`，避免与并行 package 或中断运行遗留的 schema 竞争。
- 中断的测试可能留下 `errkb_*` schema；执行数据库全局断言前应显式删除。
- 这是开发机记录，不是 Windows 安装器验收。原 EKB-052 / WF-011 结论仅属历史；现在由 WF-011A/WF-011B 保存强制托管 Windows 生命周期与确定性恢复证据。
