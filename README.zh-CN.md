# 错误知识库（Error Knowledge Base）

[English](README.md) · [许可证](LICENSE)

> **回收报错：同一个失败只诊断一次，记录下来，下次出现时作为已知解法交回 —— 全程本地完成，不调用模型。**

**项目状态（2026-10-23）：v0.1.0 已正式发布。** 可复用核心、PostgreSQL schema、CLI、后台服务、MCP 服务、已实测的 DSH/Codex/Claude 连接器和内嵌 Windows Web 工具箱均已进入首个通过验证的 Windows 版本。WF-011A/WF-011B 会在 CI 中强制验证安装生命周期与确定性恢复。可从 [v0.1.0 发布页](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/tag/v0.1.0)下载安装器或便携工具箱。

**错误知识库**（`error-knowledge-base`）是一个本地平台，有多种使用入口：命令行客户端、后台服务、MCP stdio 服务、[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 插件、宿主连接器与内嵌 Windows Web 工具箱。所有入口共用同一套核心与知识库；分类、脱敏、规范化、指纹、存储、检索和解法可信度不会在每个宿主里重复实现。命令仍是 `errkb`，DSH 适配器包继续使用 `dsh-errkb`。

## 目录

- [这是什么](#这是什么)
- [当前交付状态](#当前交付状态)
- [功能总览](#功能总览)
- [它怎么工作](#它怎么工作)
- [支持的主机（实测）](#支持的主机实测)
- [核心保证](#核心保证)
- [快速开始](#快速开始)
- [命令行](#命令行)
- [配置](#配置)
- [系统要求](#系统要求)
- [兼容性与版本口径](#兼容性与版本口径)
- [安装与卸载](#安装与卸载)
- [隐私](#隐私)
- [文档索引](#文档索引)
- [已知局限](#已知局限)
- [开发](#开发)
- [参与贡献](#参与贡献)
- [许可证](#许可证)

## 这是什么

一个面向 LLM 工具与命令行工作流的本地报错知识库。某个操作失败时，它把这次失败记录一次 —— 脱敏、规范化、指纹化 —— 存进本地 PostgreSQL 数据库。同一失败下次出现时，它交回已记录的解法，并附上匹配原因、环境不一致时的适用性警告，以及该解法实测出的可信度。

它刻意**不是**：

- **聊天模型。** 核心从不调用模型，也不发起外网请求。连接器只观察宿主已经产生的事件；解法要么本来就在数据库里，要么由用户写入。传输只包括本地 PostgreSQL、Named Pipe，以及显式启用的回环 HTTP。
- **日志仓库。** 只有够格拿编号的失败才会入库，且样本有上限；保留期是显式配置的。
- **云服务。** 没有账号、没有遥测、没有远程端点。HTTP 默认关闭，本地 API 是 Named Pipe。
- **无需外部数据库的单文件工具。** 知识库是 PostgreSQL，属于运行时前置条件（见[系统要求](#系统要求)）；Windows 安装器会创建并管理一个专用实例。

## 当前交付状态

本文刻意区分三个概念：

- **已实现**：源码和自动化测试已经存在。
- **已验证**：行为已经在注明的真实环境中运行过。
- **可发布**：同一套带版本的产物已经通过强制 Windows 生命周期/恢复与来源门禁。

| 模块 | 当前状态 | 实际含义 |
| --- | --- | --- |
| 确定性核心与脱敏 | 已实现并通过单元测试 | 可正常开发规范化、指纹、匹配与脱敏 |
| PostgreSQL schema 与检索 | 已实现；集成测试需要真实数据库 | 已有 0001–0008 迁移、精确/近似检索、解法与结果 |
| CLI、后台服务与 MCP | 已实现 | 有已迁移的 PostgreSQL 和 DSN 时，可使用开发构建运行 |
| DSH、Codex CLI、Claude Code 连接器 | 已按固定版本实测 | 只承诺连接器矩阵中已经验证的能力 |
| Windows 安装器与恢复 | 已实现；WF-011A/WF-011B 为强制 CI | 临时 Windows Runner 会运行真实安装包、迁移、知识闭环、HKCU 恢复和两种卸载选择 |
| 内嵌 Web 工具箱 | 已实现并有自动化测试覆盖 | 搜索、详情/编辑、统计/审计和设置路由均已接入 `ui --web` |
| 发布来源门禁 | 已实现并通过发布验证 | 会拒绝缺失/不匹配 tag 与脏源码；已验收 tag `v0.1.0` 指向提交 `1438473` |
| 发布包 | 已发布为 v0.1.0 | GitHub Release 包含已核对的安装器、便携工具箱、独立 EXE、manifest、SBOM 与校验和 |

## 功能总览

本项目覆盖的是完整的“技术错误复用闭环”，不只是收集日志：

| 阶段 | 用户看到的能力 | 实现边界 |
| --- | --- | --- |
| 观察 | 从 Hook、CLI 包装器、连接器和日志采集失败 | 适配器只产生 `ErrorEvent v1`，不拥有匹配算法 |
| 保护 | 在持久化之前删除敏感信息 | 一份 CUE 规则生成 Go、TypeScript 和 CI 规则 |
| 识别 | 分类、规范化、计算指纹并去重 | 确定性 Go/TypeScript 处理 + PostgreSQL 事务化记录 |
| 检索 | 先搜精确标识，再找有限的近似候选 | PL/pgSQL 精确/`pg_trgm` 查询 + Go Jaccard 重排 |
| 复用 | 返回已有解法、来源、匹配原因、环境警告和可信度 | CLI、MCP、通知与 UI 共用后端契约 |
| 学习 | 记录解法是否成功，并保存解法历史 | 版本化解法、结果记录与 SQL 计算可信度 |
| 运维 | 诊断、迁移、保留、删除、审计和备份本地数据 | Go 运维层 + PostgreSQL 函数和迁移账本 |

每项功能对应的源码位置、调用链、完成度和未接入部分，见 [`docs/features-and-implementation.zh-CN.md`](docs/features-and-implementation.zh-CN.md)。

## 它怎么工作

```text
宿主事件 ──▶ 连接器 ──▶ 脱敏 ──▶ 规范化 ──▶ 指纹 ──▶ PostgreSQL
   （Hook、包装器、MCP）        （写库之前）                    │
                                                             ▼
解法 + 原因 + 可信度 ◀── 先精确匹配，再近似候选前 3 条 ◀──── 检索
```

1. **采集。** 连接器把可观察的失败转换成 `ErrorEvent v1`：宿主报什么就给什么（退出码、命令、工具名、会话），宿主不报的由核心推导（标题、分类、严重度）。
2. **写库前脱敏。** 密钥、凭据、邮箱和绝对家目录路径被替换成占位符；规则只有一个来源（`spec/redact.cue`），生成到 Go 引擎、TypeScript 适配器和 CI 隐私守卫三处。
3. **规范化与指纹。** 去掉 ANSI 转义、GUID、时间戳、PID、计数器、端口和绝对路径；剩余文本转小写后计算 `sha256(category + "\0" + normalized)[:12]`。同一失败永远得到同一个 12 位十六进制指纹，不同失败不会相撞。
4. **存储。** 问题（按指纹与项目唯一）、出现记录、不可变事件、解法版本和结果都落进 PostgreSQL。迁移每个文件一个事务，带 advisory lock 与校验和账本。
5. **检索并交回。** 先按 issue id、指纹、错误码或命令做精确查找；模糊候选来自 `pg_trgm`，用 Jaccard 相似度重排，恒为前 3 条并且**标记为近似**。结果带匹配原因、环境差异（若有）和解法可信度；近似匹配从不自动复用。

源码级实现映射见 [`docs/features-and-implementation.zh-CN.md`](docs/features-and-implementation.zh-CN.md)；按顺序施工的任务计划见 [`docs/implementation-plan.zh-CN.md`](docs/implementation-plan.zh-CN.md)。

## 支持的主机（实测）

实测于 2026-10-20；版本刻意固定。每一行都对应一条真实验收记录；没有实测的内容一律列为未验证，见 [`docs/connector-matrix.zh-CN.md`](docs/connector-matrix.zh-CN.md)。

| 宿主 | 实测版本 | 通道 | 诊断前能拿到已知解法 | 验收记录 |
| --- | --- | --- | --- | --- |
| 通用 CLI（`errkb run --`） | 本仓库构建的 errkb | **B** 包装器 | 否 —— 失败之后 | [`docs/e2e-cli-loop.zh-CN.md`](docs/e2e-cli-loop.zh-CN.md) |
| DSH（插件适配器） | DSH 0.2.0-rc.2 | **A** 插件 Hook | 是 —— 常驻提示与执行后通知 | EKB-039，PR #66 |
| Codex CLI | 0.156.1 | **B** 包装器；**MCP** 八个工具 | 否 —— MCP 工具可主动查询，但没有失败前 Hook | [`connectors/codex/probe.md`](connectors/codex/probe.md) |
| Claude Code | 2.1.282 | **A** `PostToolUseFailure` Hook；**B** 包装器；**MCP** | 是 —— `SessionStart` 提示与 MCP 工具 | [`connectors/claude/probe.md`](connectors/claude/probe.md) |

通用日志与手工宿主使用 [`connectors/templates/cli-log/`](connectors/templates/cli-log/README.md) 的模板；它们的日志通道与手工通道尚未实测。窗口标题不是接口契约，这里的支持声明没有一条建立在窗口标题上。

## 核心保证

- **先精确、后模糊。** 检索门槛先要一个完整取值 —— issue id、指纹、错误码、规范化文本、命令或工具名 —— 过了门槛才放宽。
- **零错误自动合并。** 只有「指纹精确相等，且错误码、命令、工具不矛盾」才允许自动复用；标注语料（`spec/corpus/labelled/`）里专门放了朴素匹配会合并的近似样本，测试断言自动合并数为零。
- **近似匹配可见。** 界面打印 `APPROXIMATE — confirm before reuse`；通知带近似标记与环境警告；MCP 与 CLI 的 JSON 带 `approximate: true`。
- **最多三条，且带原因。** 检索最多返回 3 个候选，每个都带匹配原因和命中的字段。
- **延迟是门槛，不是口号。** 参考机上 10 万 issue 规模：精确查找 p95 0.6 ms，trigram 候选 p95 81.7 ms，完整检索 p95 84.5 ms（门槛 150 ms）。环境、数据集与查询计划见 [`docs/benchmarks.zh-CN.md`](docs/benchmarks.zh-CN.md)。
- **可信度是算出来的。** `errkb_trust()` = `(successes + 2) / (successes + failures + 4)`，在 SQL 里根据已记录的结果计算；未知结果不计入。界面只展示，不能写入。
- **写入幂等。** 事件按 `event_id` 唯一；连接器观测带 `observation_id`；重放不会产生重复，出现记录则总是追加。
- **不会把产品搞崩。** 后台服务每个 Windows 会话只能起一个，优雅退出，写健康文档，日志先脱敏再按大小轮转。写入、批量写入或迁移中途崩溃，已提交数据保持完整（测试里真的杀进程验证过）。

## 快速开始

> 以下是**开发工作区**流程，目前还没有受支持的一键安装版本。不要混用 `dist\errkb.exe` 与 `dist\release\...`，它们来自不同修订。

1. **安装 PostgreSQL 16**（EDB 安装包，或让 errkb 安装器在 5433 端口创建专用实例）。记下 DSN。
2. **构建当前源码并应用 schema：**

   ```powershell
   go build -o errkb.exe ./cmd/errkb
   .\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
   ```

3. **启动后台服务：**

   ```powershell
   .\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable"
   ```

4. **用包装器采集一次真实失败：**

   ```powershell
   .\errkb.exe run --connector cli --session demo --project demo -- cmd /c "exit 9"
   ```

5. **记录解法** —— 通过 MCP 服务（`err_record_solution`）或连接器 —— 再跑同一条命令，包装器就会打印已知解法及其来源与可信度。

v0.1.0 安装器和便携工具箱都可以通过 `errkb ui --web` 打开内嵌工具箱；daemon 缺失时，它会解析安装凭据与 migrations 进行恢复。

含真实输出的完整闭环见 [`docs/e2e-cli-loop.zh-CN.md`](docs/e2e-cli-loop.zh-CN.md)。

## 命令行

一个二进制，多个子命令：

| 命令 | 作用 |
| --- | --- |
| `errkb search <query>` | 按 issue id、指纹、错误码、命令或文本检索；人读表格或 `--json` |
| `errkb show <id-or-fingerprint>` | 查看单个问题及其出现记录、解法和版本 |
| `errkb record --event <file>` | 记录完整的 `ErrorEvent v1` 载荷（stdin 或文件） |
| `errkb list --kind issue\|solution` | 列出问题或解法，可按项目过滤 |
| `errkb feedback <issue> <version> --outcome success\|failure\|unknown` | 记录结果；可信度就靠它变化 |
| `errkb stats` | 计数、命中率、延迟百分位与估算节省 |
| `errkb run -- <command>` | 运行命令、保持输出可见、只记录失败 |
| `errkb db migrate\|status` | 应用迁移或查看 schema 账本 |
| `errkb tail --file <path>` | 跟随日志文件并记录其中的失败（通道 C） |
| `errkb markdown import\|export` | 导入或导出旧版 `ERRORS.md` 格式 |
| `errkb daemon` | 本地后台服务：Named Pipe API、健康文档、脱敏日志 |
| `errkb ui` | 源码树中的原生托盘/搜索宿主；不是已发布的 Web 工具箱 |
| `errkb ui --web` | 本地开发中的 Web 搜索壳；当前发布物不包含 |
| `errkb doctor` | 八项只读环境与连接器检查，每条发现附修复建议 |
| `errkb mcp` | 带八个工具的 MCP stdio 服务 |
| `errkb version` | 版本、提交、构建时间与 Go 工具链 |

退出码：`0` 找到/成功，`1` 内部错误，`2` 参数错误，`3` 未找到，`4` 后台服务不可用。`--json` 输出的是校验过的契约载荷，不是二次渲染的近似物。

## 配置

配置是一份 YAML 文档，只由一个契约（`spec/config.cue`）校验：

```yaml
database:
  dsn: postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable
service:
  pipe:
    enabled: true
  http:
    enabled: false        # 启用时只允许回环地址，令牌至少 32 个字符
capture:
  enabled: true
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
```

默认值：Named Pipe 开、HTTP 关、采集开、通知开、检索上限 3、采集/检索/MCP 超时 2000/1500/5000 ms。后台服务也接受显式参数（`--dsn`、`--pipe`、`--health-file`、`--log-file` 等）；设置界面模型编辑的是同一份文件，不引入第二套配置模型。完整示例见 [`connectors/templates/cli-log/config.yaml`](connectors/templates/cli-log/config.yaml)。

## 系统要求

- **Windows 10 或 11，x64。** 后台服务、托盘、Named Pipe API 与 Windows 连接器服务都是 Windows 专用。
- **PostgreSQL 16**（服务端）。不随附；安装器可以在 5433 端口创建专用实例，也可以复用已有本地实例。
- **运行时不需要 Go、Node.js 或开发工具链。** 发布二进制与安装器除 PostgreSQL 外自给自足。
- 仅开发需要：Go 1.26.4、Node.js 24.16.0（pnpm 11.7.0）、CUE 0.17.1、Inno Setup 6，以及一个本地 PostgreSQL 用于数据库测试。

## 兼容性与版本口径

- **schema。** v1，迁移 `0001`–`0008`。旧 schema 升级用 `db/migrations/testdata/upgrade/` 中的真实快照测试；更新的账本版本会被显式拒绝，失败的迁移在单个事务内回滚。
- **契约。** `ErrorEvent v1`、配置、连接器能力、MCP 工具、本地 API、连接器 SDK 与脱敏规则都有 CUE 定义。兼容性由 `spec/compat.cue` 和 `Schema compatibility` CI 作业把关：新增可选字段、新增枚举成员属于兼容；删除、改名或改类型必须升 `schema_version`，违反规则作业直接失败。
- **DSH 依赖包。** 插件按锁文件解析的 `@deepseek-ai/dsh-llm`、`@deepseek-ai/dsh-tools` **0.1.5-rc.3** 构建，宿主为 **0.2.x** 世代（实测 0.2.0-rc.2；计划里以 `dsh-ops` 的 peer 范围作为版本证据）。早先写的 rc.2 是设计期历史口径：peer 范围 `^0.1.5-rc.2` 覆盖 rc.3，且适配器用到的 API 面没有变化。完整说明见计划 §7.2。
- **协议。** MCP `2024-11-05`；本地 API 与连接器 SDK 的封套都是 `v1`。解码后的载荷拒绝未知字段；声明更新版本的载荷会被拒绝，而不是猜测处理。

## 安装与卸载

正式安装器按用户安装到 `%LOCALAPPDATA%\errkb`，可在 5433 端口创建专用 PostgreSQL 实例、应用迁移、登记当前用户登录恢复、安装 `start-errkb.cmd` 并创建开始菜单工具箱快捷方式；卸载时可以保留或删除数据。WF-011A/WF-011B 会在 Windows CI 中验证这些路径。请只使用 v0.1.0 GitHub Release 附带的文件；本地忽略的 `dist/` 仍是可丢弃的构建输出。

完整步骤、数据位置与静默安装行为见 [`docs/install.md`](docs/install.md)。强制 Windows CI 验收与可选干净机检查见 [`docs/acceptance-vm.zh-CN.md`](docs/acceptance-vm.zh-CN.md)。发布物未做代码签名；请用发布包里的 `SHA256SUMS` 校验。

## 隐私

一切都留在本机。核心从不调用模型，也不发起外网请求；本地 API 是带「仅所有者」ACL 的 Named Pipe，HTTP 默认关闭，脱敏在**写入知识库之前**完成。三类保留期、带预览与审计的定向删除，以及适配器离线队列保留了什么都写在 [`docs/privacy.zh-CN.md`](docs/privacy.zh-CN.md)。

## 文档索引

| 文档 | 回答什么 |
| --- | --- |
| [`docs/project-status.zh-CN.md`](docs/project-status.zh-CN.md) / [English](docs/project-status.md) | 当前哪些已实现、已验证、阻塞，以及现在能安全运行什么 |
| [`docs/workflow/README.zh-CN.md`](docs/workflow/README.zh-CN.md) / [English](docs/workflow/README.md) | 工作流控制中心：活动问题、文件修复顺序和清理审计 |
| [`docs/features-and-implementation.zh-CN.md`](docs/features-and-implementation.zh-CN.md) / [English](docs/features-and-implementation.md) | 每项功能做什么，以及由哪些源码、契约和数据链路实现 |
| [`docs/implementation-plan.zh-CN.md`](docs/implementation-plan.zh-CN.md) / [English](docs/implementation-plan.md) | 范围、决定、任务顺序和验收门槛 |
| [`docs/connector-matrix.zh-CN.md`](docs/connector-matrix.zh-CN.md) / [English](docs/connector-matrix.md) | 每个宿主到底报哪些字段，版本固定并实测 |
| [`docs/install.zh-CN.md`](docs/install.zh-CN.md) / [English](docs/install.md) | 当前安装候选、数据位置、升级和卸载 |
| [`docs/toolbox-ui.zh-CN.md`](docs/toolbox-ui.zh-CN.md) / [English](docs/toolbox-ui.md) | 工具箱 UI 架构、实施状态和剩余任务 |
| [`docs/privacy.zh-CN.md`](docs/privacy.zh-CN.md) / [English](docs/privacy.md) | 数据流向、脱敏、保留期、删除 |
| [`docs/benchmarks.zh-CN.md`](docs/benchmarks.zh-CN.md) / [English](docs/benchmarks.md) | 性能门槛、数据集、查询计划、环境 |
| [`docs/test-record.zh-CN.md`](docs/test-record.zh-CN.md) / [English](docs/test-record.md) | 长期保存的完整测试与 Windows 验收记录 |
| [`docs/acceptance-vm.zh-CN.md`](docs/acceptance-vm.zh-CN.md) / [English](docs/acceptance-vm.md) | 强制 Windows CI 验收、证据与可选人工检查 |
| [`docs/e2e-cli-loop.zh-CN.md`](docs/e2e-cli-loop.zh-CN.md) / [English](docs/e2e-cli-loop.md) | 通用 CLI 闭环，全程不经过模型 |
| [`CHANGELOG.md`](CHANGELOG.md) | 每个版本改了什么 |
| [`packaging/windows/`](packaging/windows) | 构建、发布与安装脚本 |

## 已知局限

以下是仍然存在的局限或刻意保留的边界，不是隐藏能力：

- **真实重启与交互式干净机检查没有持续自动化。** WF-011B 会确定性测试等价的 daemon/PostgreSQL/登录恢复；真实重启和安装向导视觉检查保留为可选人工证据。
- **发布物未做代码签名。** SmartScreen 可能告警；请核对 `SHA256SUMS`。
- **PostgreSQL 是前置条件。** 核心需要本地 PostgreSQL 16 服务；安装器会创建实例，但数据库不内嵌在程序里。
- **DSH 插件仍在等待商店审核。** 目前以手动安装为受支持路径。
- **连接器矩阵里有两处实测缺口：** DSH 的 `tools/result` 实时路径与 Codex 的 `PostToolUse` Hook 尚未验证。

## 开发

```powershell
pnpm install
pnpm typecheck     # tsc --noEmit
pnpm lint          # oxlint
pnpm format:check  # prettier
pnpm test          # vitest，99% 覆盖率门槛
pnpm build         # DSH 连接器产物 lib/

go build ./...
go vet ./...
gofmt -l .
cue vet ./spec/...
```

数据库测试需要本地 PostgreSQL 与 `ERRKB_TEST_DATABASE_URL`；没设置时会跳过。构建、发布与安装脚本在 [`packaging/windows/`](packaging/windows)；逐卡施工顺序见计划 §11.1；仓库约定（一张卡一个 PR、两种语言同步改动）见计划。

## 参与贡献

先读实施计划；工作按任务卡（`EKB-nnn`）推进，一张卡一个 PR，跑计划 §12 的门槛。两份计划与两份 README 必须保持同步。安全或隐私问题请开 issue 并指明受影响的文件，不要在公开修复提交里描述。

## 许可证

[MIT](LICENSE) © jingchangzhao-gif
