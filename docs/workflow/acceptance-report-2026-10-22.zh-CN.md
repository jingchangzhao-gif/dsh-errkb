# 验收报告——2026-10-22

[English](acceptance-report-2026-10-22.md)

状态：**未通过——禁止发布**

> 历史记录：这是提交 `2571903` 上的首次验收。文中 WF-011 干净 VM 结论已经由强制 `windows-acceptance` CI 作业中的 WF-011A/WF-011B 取代；本文保留原样作为当时证据，当前状态以 [`../project-status.zh-CN.md`](../project-status.zh-CN.md) 与 [`current-workflow.zh-CN.md`](current-workflow.zh-CN.md) 为准。

本文记录实施序列完成后的第一轮验收，明确区分已经验证的产品行为、仍未通过的测试门禁、文档门禁和环境门禁。报告不会保存数据库明文凭据。

## 源码与产物身份

| 项目 | 实测值 | 结果 |
| --- | --- | --- |
| 工作分支 | `local/work-state-2026-10-22` | 通过 |
| HEAD | `257190376164e7c16bccdcef212b5f2bbbb8c28b` | 通过 |
| 验收前 Git 状态 | 干净 | 通过 |
| 发布包 | `dist/release/errkb-0.1.0` | 存在 |
| 包内 EXE 身份 | `0.1.0`，提交 `fa2585e`，构建时间 `2026-10-22T00:00:00Z` | 通过 |
| 产物提交之后的改动 | 只有 3 个验收/工作流 Markdown 文件 | 通过 |
| Git tag `v0.1.0` | 不存在 | **失败** |

## 自动化门禁

| 门禁 | 结果 | 证据 |
| --- | --- | --- |
| `pnpm typecheck` | 通过 | 退出码 0 |
| `pnpm lint` | 带警告通过 | 退出码 0；10 条未使用变量/生成代码警告 |
| `pnpm test` | 通过 | 16 个文件、733 项测试；语句/行覆盖率 99.4% |
| `pnpm format:check` | 通过 | 所有匹配文件格式正确 |
| `pnpm build` | 通过 | 重新生成 `lib/index.js`、source map 和声明文件 |
| `go vet ./...` | 通过 | 退出码 0 |
| 不带真实 DSN 的 `go test ./... -count=1` | 通过 | 所有非数据库门禁通过；数据库测试按设计跳过 |
| `cue vet ./spec/...` | 通过 | 退出码 0 |
| Windows 构建脚本 | 通过 | 临时 `0.1.0` EXE 报告 HEAD `2571903`，校验和一致 |

## 真实 PostgreSQL 验收

本机 PostgreSQL 16 正在运行。测试 DSN 来自已记录的开发机配置，只传给子测试进程，没有写入报告。

| 门禁 | 结果 | 证据 |
| --- | --- | --- |
| 带真实 DSN 的 `go test ./... -count=1` | 通过 | 连续 3/3 次全新并行运行通过；数据库、服务、Web 壳和 E2E 等全部包通过 |
| `go test ./internal/database/ -run '^TestCrash' -count=3` | 通过 | 连续 3/3 通过，约 30 秒 |
| 无关锁回归测试 | 通过 | 无关后端持有的 advisory lock 被忽略；归属于 crash helper 的锁仍会被检出 |

结论（WF-013 已解决）：崩溃断言已经从统计整个数据库改为只检查被终止的 helper 后端。文档规定的并行发布门禁无需 `-p 1` 即可重复通过。

## 发布包检查

| 检查 | 结果 |
| --- | --- |
| `SHA256SUMS` 中 9 个条目 | 全部通过 |
| 安装器 artifacts 阶段 | 3 通过、0 失败 |
| 便携 ZIP 白名单 | 通过：`CHANGELOG.md`、`errkb.exe`、`LICENSE`、`README.md`、`start-errkb.cmd` |
| ZIP 中的凭据、健康数据、日志或 PostgreSQL 数据 | 未发现 |
| Authenticode 签名 | 未签名；符合当前已记录的签名策略 |
| `release.ps1 -Mode check -Version 0.1.0 -Tag v0.1.0 -VmAcceptanceVersion 0.1.0` | Git tag 不存在时仍返回 OK |

结论：`Resolve-ReleaseTag` 只检查传入文本是否等于 `v<version>`，没有解析 Git ref，也没有比较 tag 与已验收提交。这个发布门禁会产生假阳性，已经加入 WF-012。

## 机器验收边界

本机 `artifacts` 阶段 3/3 通过。`preflight` 彩排只有在使用 `-AllowAdmin -AllowDirtyHost` 时 5/5 通过，不能作为干净 VM 证据。本轮没有在开发机执行安装、真实标准用户会话、重启恢复、交互式启动器和两种卸载。WF-011 继续等待干净 Windows VM。

## 文档一致性

结论（WF-014 已解决）：`docs/project-status.*` 及其关联的当前状态摘要已经依据受 Git 跟踪的工作区、CLI help、Web 路由、安装器源码、发布 manifest 与本报告同步。中英文均已删除过时的工作区、启动器、安装凭据和 Web 页面阻塞项。

## 验收决定

当前实现可以继续进行后续验收，但 **0.1.0 暂不接受发布**。先在干净 VM 完成 WF-011，再由 WF-012 使用真实的已验收 Git tag 只重建一次最终发布包。
