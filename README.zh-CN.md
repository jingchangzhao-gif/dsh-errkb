# 错误知识库（Error Knowledge Base）

[English](README.md) · [许可证](LICENSE)

> **回收报错：同一个失败只诊断一次，记录下来，下次出现时作为已知解法交回 —— 全程本地完成，不调用模型。**

**状态：v0.1.0（2026-10-21）。** Windows 桌面构建，本地 PostgreSQL 16 知识库；已对 DSH 0.2.0-rc.2、Codex CLI 0.156.1、Claude Code 2.1.282 实测。干净虚拟机验收**阻塞**，记录在 [`docs/acceptance-vm.md`](docs/acceptance-vm.md)；发布流水线在没有该验收记录时拒绝发布版本。

**错误知识库**（`error-knowledge-base`）是同一产品的多种形态：命令行客户端、本地后台服务、MCP stdio 服务、[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 插件，以及面向 Codex CLI、Claude Code 的宿主连接器。所有形态共用同一个本地核心和同一个知识库：分类、脱敏、规范化、指纹、存储、检索和解法可信度都只在核心里实现一次，不在各宿主里重复实现。仓库名仍为 `dsh-errkb`，命令仍是 `errkb`，两者继续可用。

## 目录

- [这是什么](#这是什么)
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

- **聊天模型。** 核心从不调用模型，也不使用网络。连接器只观察宿主已经产生的事件；解法要么本来就在数据库里，要么由用户写入。
- **日志仓库。** 只有够格拿编号的失败才会入库，且样本有上限；保留期是显式配置的。
- **云服务。** 没有账号、没有遥测、没有远程端点。HTTP 默认关闭，本地 API 是 Named Pipe。
- **无需外部数据库的单文件工具。** 知识库是 PostgreSQL，属于运行时前置条件（见[系统要求](#系统要求)）；Windows 安装器会创建并管理一个专用实例。

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

完整设计见 [`docs/implementation-plan.zh-CN.md`](docs/implementation-plan.zh-CN.md)。

## 支持的主机（实测）

实测于 2026-10-20；版本刻意固定。每一行都对应一条真实验收记录；没有实测的内容一律列为未验证，见 [`docs/connector-matrix.zh-CN.md`](docs/connector-matrix.zh-CN.md)。

| 宿主 | 实测版本 | 通道 | 诊断前能拿到已知解法 | 验收记录 |
| --- | --- | --- | --- | --- |
| 通用 CLI（`errkb run --`） | 本仓库构建的 errkb | **B** 包装器 | 否 —— 失败之后 | [`docs/e2e-cli-loop.md`](docs/e2e-cli-loop.md) |
| DSH（插件适配器） | DSH 0.2.0-rc.2 | **A** 插件 Hook | 是 —— 常驻提示与执行后通知 | EKB-039，PR #66 |
| Codex CLI | 0.156.1 | **B** 包装器；**MCP** 八个工具 | 否 —— MCP 工具可主动查询，但没有失败前 Hook | [`connectors/codex/probe.md`](connectors/codex/probe.md) |
| Claude Code | 2.1.282 | **A** `PostToolUseFailure` Hook；**B** 包装器；**MCP** | 是 —— `SessionStart` 提示与 MCP 工具 | [`connectors/claude/probe.md`](connectors/claude/probe.md) |

通用日志与手工宿主使用 [`connectors/templates/cli-log/`](connectors/templates/cli-log/README.md) 的模板；它们的日志通道与手工通道尚未实测。窗口标题不是接口契约，这里的支持声明没有一条建立在窗口标题上。

## 核心保证

- **先精确、后模糊。** 检索门槛先要一个完整取值 —— issue id、指纹、错误码、规范化文本、命令或工具名 —— 过了门槛才放宽。
- **零错误自动合并。** 只有「指纹精确相等，且错误码、命令、工具不矛盾」才允许自动复用；标注语料（`spec/corpus/labelled/`）里专门放了朴素匹配会合并的近似样本，测试断言自动合并数为零。
- **近似匹配可见。** 界面打印 `APPROXIMATE — confirm before reuse`；通知带近似标记与环境警告；MCP 与 CLI 的 JSON 带 `approximate: true`。
- **最多三条，且带原因。** 检索最多返回 3 个候选，每个都带匹配原因和命中的字段。
- **延迟是门槛，不是口号。** 参考机上 10 万 issue 规模：精确查找 p95 0.6 ms，trigram 候选 p95 81.7 ms，完整检索 p95 84.5 ms（门槛 150 ms）。环境、数据集与查询计划见 [`docs/benchmarks.md`](docs/benchmarks.md)。
- **可信度是算出来的。** `errkb_trust()` = `(successes + 2) / (successes + failures + 4)`，在 SQL 里根据已记录的结果计算；未知结果不计入。界面只展示，不能写入。
- **写入幂等。** 事件按 `event_id` 唯一；连接器观测带 `observation_id`；重放不会产生重复，出现记录则总是追加。
- **不会把产品搞崩。** 后台服务每个 Windows 会话只能起一个，优雅退出，写健康文档，日志先脱敏再按大小轮转。写入、批量写入或迁移中途崩溃，已提交数据保持完整（测试里真的杀进程验证过）。

## 快速开始

> 0.1.0 的「安装后首次运行」路径**还不完整**：安装器会创建 PostgreSQL 实例，但不会应用 schema，CLI 也还没有 `db migrate` 子命令。见[已知局限](#已知局限)。在后续卡补上这个缺口之前，受支持的路线是 [`docs/acceptance-vm.md`](docs/acceptance-vm.md) 记录下的开发/演练路径。

1. **安装 PostgreSQL 16**（EDB 安装包，或让 errkb 安装器在 5433 端口创建专用实例）。记下 DSN。
2. **把 schema 应用到该数据库。** 0.1.0 里由 Go 迁移器完成（测试与演练走的就是这条路径）。
3. **构建并启动后台服务：**

   ```powershell
   go build -o errkb.exe ./cmd/errkb
   .\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable"
   ```

4. **用包装器采集一次真实失败：**

   ```powershell
   .\errkb.exe run --connector cli --session demo --project demo -- cmd /c "exit 9"
   ```

5. **记录解法** —— 通过 MCP 服务（`err_record_solution`）、DSH 插件或编辑界面模型 —— 再跑同一条命令，包装器就会打印已知解法及其来源与可信度。

含真实输出的完整闭环见 [`docs/e2e-cli-loop.md`](docs/e2e-cli-loop.md)。

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
| `errkb daemon` | 本地后台服务：Named Pipe API、健康文档、脱敏日志 |
| `errkb ui` | 托盘与搜索窗口，连接后台服务 |
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

Windows 安装器（`errkb-<version>-setup.exe`）按用户安装到 `%LOCALAPPDATA%\errkb`，在 5433 端口创建专用 PostgreSQL 实例（也可复用已有实例或另选端口），并提供启动选项。卸载时会询问**保留数据**还是**删除数据**；实例密码保存在 `%LOCALAPPDATA%\errkb\pg\credentials.json`，只有当前用户可读。

完整步骤、数据位置、静默安装行为与当前首次运行缺口见 [`docs/install.md`](docs/install.md)。干净虚拟机验收处于阻塞状态，其发现与验收脚本见 [`docs/acceptance-vm.md`](docs/acceptance-vm.md)。发布物未做代码签名；请用发布包里的 `SHA256SUMS` 校验。

## 隐私

一切都留在本机。核心从不调用模型、从不使用网络；本地 API 是带「仅所有者」ACL 的 Named Pipe，HTTP 默认关闭，脱敏在**写入知识库之前**完成。三类保留期、带预览与审计的定向删除，以及适配器离线队列保留了什么都写在 [`docs/privacy.md`](docs/privacy.md)。

## 文档索引

| 文档 | 回答什么 |
| --- | --- |
| [`docs/implementation-plan.zh-CN.md`](docs/implementation-plan.zh-CN.md) / [English](docs/implementation-plan.md) | 唯一事实源：范围、决定、任务卡、验收门槛 |
| [`docs/connector-matrix.zh-CN.md`](docs/connector-matrix.zh-CN.md) / [English](docs/connector-matrix.md) | 每个宿主到底报哪些字段，版本固定并实测 |
| [`docs/install.md`](docs/install.md) | 安装、数据位置、升级、卸载 |
| [`docs/privacy.md`](docs/privacy.md) | 数据流向、脱敏、保留期、删除 |
| [`docs/benchmarks.md`](docs/benchmarks.md) | 性能门槛、数据集、查询计划、环境 |
| [`docs/acceptance-vm.md`](docs/acceptance-vm.md) | 干净虚拟机验收结果与当前阻塞项 |
| [`docs/e2e-cli-loop.md`](docs/e2e-cli-loop.md) | 通用 CLI 闭环，全程不经过模型 |
| [`CHANGELOG.md`](CHANGELOG.md) | 每个版本改了什么 |
| [`packaging/windows/`](packaging/windows) | 构建、发布与安装脚本 |

## 已知局限

以下问题都记录在 [`CHANGELOG.md`](CHANGELOG.md)，并由计划跟踪；它们不会被藏起来。

- **全新安装没有 schema。** 安装器创建实例但不应用迁移，CLI 尚无 `db migrate` 子命令；账本不存在时后台服务拒绝启动。
- **重启后不会自启。** 实例没有注册成 Windows 服务，也没有后台服务/托盘的开机自启。
- **发布物未做代码签名。** SmartScreen 可能告警；请核对 `SHA256SUMS`。
- **PostgreSQL 是前置条件。** 核心需要本地 PostgreSQL 16 服务；安装器会创建实例，但数据库不内嵌在程序里。
- **部分界面只是模型。** 托盘与搜索窗口已接到运行中的后台服务；问题详情、设置与统计是有测试的模型，窗口尚未接上。
- **`errkb run -- <command>` 在 `--` 前不带任何参数时**会误报「命令必须跟在 `--` 之后」。适配器会先传 `--connector`、`--session`，照做即可绕开。
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
