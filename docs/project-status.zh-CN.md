# 项目当前状态

[English](project-status.md)

状态日期：2026-10-23

本文是当前交付事实的权威入口：说明现有 Git 工作区中已经存在什么、哪些内容已经实测、哪些内容已经公开发布，以及仍有哪些限制。历史任务记录只说明项目如何走到当前状态，不能覆盖本文。

## 一句话结论

错误知识库已经从 DeepSeek 专用插件升级为本地、多宿主的错误复用工具箱：

```text
DSH / Codex / Claude / 通用 CLI / MCP
                    │
                    ▼
       采集 → 脱敏 → 规范化 → 指纹
                    │
                    ▼
              PostgreSQL 知识库
                    │
                    ▼
          精确检索 → 近似前 3 条 → 已知解法
```

可复用核心、PostgreSQL schema、CLI、后台服务、MCP 服务、首批宿主连接器与内嵌 Windows Web 工具箱均已实现。v0.1.1 安装器包含受管 PostgreSQL、迁移、登录恢复、启动器、开始菜单快捷方式以及两种卸载选择。WF-011A/WF-011B 已把安装生命周期与恢复变为强制 Windows CI 门禁。WF-012 已于 2026-10-23 发布通过验证的 tag 和发布包，因此 v0.1.1 是首个受支持的公开版本。

## 状态用语

| 用语 | 含义 |
| --- | --- |
| 已实现 | 已有源码和自动化测试 |
| 已验证 | 已在注明环境中成功运行 |
| 已验收 | 功能专属、公共和必要人工门槛全部通过 |
| 可发布 | 同一个带 tag 产物通过强制 Windows 生命周期/恢复门禁与来源检查 |

“已实现”不自动等于“已验证”“已验收”或“可发布”。

## 当前已经实现

- 确定性的分类、标题提取、脱敏、规范化与指纹计算。
- PostgreSQL 迁移 `0001`–`0008`、幂等记录、解法版本、结果反馈、可信度、精确检索与 trigram/Jaccard 检索。
- 带所有者范围 Named Pipe、健康文件、脱敏日志和单实例锁的 Windows 后台服务。
- CLI 搜索、查看、记录、列表、反馈、统计、命令包装、日志跟随、Markdown 交换、数据库/PostgreSQL 管理、doctor、MCP、UI 与版本命令。
- MCP stdio，以及已经记录的 DSH、Codex CLI、Claude Code 与通用 CLI 接入路径。
- 内嵌回环 Web 工具箱：搜索、问题详情、解法/状态写入、统计、审计、连接器/隐私设置与定向删除。
- 基于安装凭据恢复 daemon：`ui --web` 会解析 `pg/credentials.json` 与安装目录中的 migrations，再拉起缺失的 daemon。
- 按用户安装的 Windows 安装器源码：schema 迁移、登录恢复、启动器、开始菜单快捷方式、保留数据卸载和删除数据卸载。
- 发布来源门禁：拒绝不存在的 tag、指向其他提交的 tag、脏工作区与显式 commit 不一致。

## 当前已经验证

| 范围 | 证据 |
| --- | --- |
| TypeScript 适配器 | 2026-10-22 验收运行：733 项测试，语句/行覆盖率 99.4% |
| Go 与 PostgreSQL | WF-013 后，PostgreSQL 16 上 package 并行的完整 `go test ./... -count=1` 连续 3 次全新运行通过 |
| 崩溃安全 | 四种真实杀进程场景通过；回归测试会忽略无关 advisory lock，同时仍能检出 crash-helper 的锁 |
| CUE 契约 | 本机 `cue vet ./spec/...` 通过 |
| 仓库质量 | 已记录的验收运行中 typecheck、lint、format、build 与 `go vet ./...` 通过 |
| CLI 复用闭环 | 真实数据库的记录 → 搜索 → 解法 → 复用 → 反馈证据见 `e2e-cli-loop.zh-CN.md` |
| DSH / Codex / Claude 连接器 | 实测版本和未验证单元均明确记录在 `connector-matrix.zh-CN.md` |
| 安装器/发布彩排 | 开发机上的产物检查、安装迁移/登录恢复与 5 项来源门禁测试均通过 |
| Windows 生命周期/恢复 | WF-011A/WF-011B 在临时 Windows Runner 上构建真实安装包，验证安装/复用/保留/删除与 daemon/PostgreSQL 确定性恢复，并上传结构化证据 |

自动门禁不宣称完成了真实整机重启或交互窗口视觉检查；这些保留为可选人工证据。

## 当前发布边界

1. **v0.1.1——已发布并核对。** `v0.1.1` tag 指向提交 `a60a32f`；标签工作流重跑 WF-011A/WF-011B，并发布安装器、工具箱 ZIP、独立 EXE、manifest、SBOM 与哈希。下载后的发布包身份与全部 `SHA256SUMS` 条目均已核对。
2. **签名限制——已接受但必须显式展示。** 正式版本尚未做 Authenticode 签名。这不会绕过校验和与来源门禁；引入签名证书前，用户必须核对 `SHA256SUMS`。

WF-013（并行数据库确定性）与 WF-014（交付状态同步）均已完成。

## 当前安全使用方式

### 开发与验收

从当前工作区构建并使用显式 PostgreSQL 16 DSN，或通过 Windows 验收脚本运行安装器。可以验证 CLI、MCP 服务和已实测连接器；不得把彩排发布包描述成正式版本。

```powershell
go build -o errkb.exe ./cmd/errkb
.\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe ui --web --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
```

### 最终用户安装

从 [v0.1.1 发布页](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/tag/v0.1.1) 下载 `errkb-0.1.1-setup.exe`，使用 `SHA256SUMS` 核对安装器，然后以标准用户运行。`errkb-toolbox.zip` 是便携替代方案。

## 发布检查表

只有以下答案全部为“是”，才可以发布：

- `errkb version` 是否显示目标 tag 和已验收 commit？
- 安装器中是否就是该 EXE，安装后 EXE 是否具有相同哈希？
- 开始菜单快捷方式是否能在不显示终端的情况下打开工具箱？
- 工具箱能否使用安装凭据启动或重新连接受管 PostgreSQL 与 daemon？
- 搜索、详情/编辑、统计/审计和设置是否通过预期本地接口工作？
- 同一个已知错误是否能在不调用模型的情况下返回已记录解法？
- WF-011B 是否在确定性进程故障后恢复唯一 daemon、集群、Named Pipe、健康状态与已保存知识？
- 保留数据和删除数据两种卸载是否符合文档？
- 数据库集成测试、格式/隐私检查与发布哈希是否全部通过？
- 中英文文档是否同步？

## 文档职责

| 文档 | 用途 |
| --- | --- |
| `project-status.zh-CN.md` | 当前交付事实与发布边界 |
| `workflow/README.zh-CN.md` | 活动工作控制中心与修复顺序 |
| `features-and-implementation.zh-CN.md` | 用户功能与实现路径 |
| `implementation-plan.zh-CN.md` | 产品决定、任务顺序与验收标准 |
| `toolbox-ui.zh-CN.md` | UI 架构、路由与交付证据 |
| `acceptance-vm.zh-CN.md` | 强制 Windows CI 验收与可选干净机检查 |
| `connector-matrix.zh-CN.md` | 宿主/版本能力证据 |
| `README.zh-CN.md` | 项目简明入口 |
