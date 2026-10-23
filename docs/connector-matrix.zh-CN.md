# 连接器兼容矩阵

每个宿主实际能给知识库什么，以实测为准，不靠推测。没有实测来源的格子只标
**未验证**，绝不写成支持；窗口标题不是接口契约（计划 §3.2），本表没有任何
一行依赖它。

测量时间：**2026-10-20**，测量环境：

| 事实 | 值 |
| --- | --- |
| errkb | 本仓库 `main` `84a151d4f38c` 加 EKB-042 分支（PR #69，`dcb43ad`），`go1.26.4` windows/amd64，包装器自报 `errkb dev` |
| DSH 宿主 | 0.2.0-rc.2（桌面宿主自带），插件 `dsh-errkb` 来自同一棵树 |
| Codex CLI | 0.156.1 |
| Claude Code | 2.1.282 |
| 机器 | Windows 11 Pro for Workstations 10.0.26200，PostgreSQL 16.15（`127.0.0.1:5432`） |

版本刻意写死：宿主升级可能改 Hook 名称、载荷结构或退出码约定，届时矩阵必须
重新测量，而不是沿用旧结论。

## 图例

| 标记 | 含义 |
| --- | --- |
| ✅ | 实测：宿主或包装器**结构化**提供该字段。 |
| 🟡 | 核心从文本派生（标题、分类、脱敏）或从标记解析；值存在，但不是宿主的结构化字段。 |
| ⬜ | 该通道拿不到。 |
| ❓ | 尚未测量（列为缺口，绝不写成支持）。 |

`ErrorEvent v1` 的可选字段（`error_code`、`command`、`exit_code`、`tool_name`、
`shell`、`runtime_versions`）宿主不给就缺省；必填字段由核心流水线保证。

## 宿主总览

| 宿主 | 实测版本 | 通道 | 诊断前回传 | 降级方式 | 验收记录 |
| --- | --- | --- | --- | --- | --- |
| 通用 CLI（`errkb run --`） | 本树构建的 errkb，见证运行 `errkb dev`（2026-10-19） | **B** 包装器 | 否——失败后才打印已知解法 | 1) 包装下一条命令；2) 从宿主日志取一行交给包装器（C 意图，日志尾随尚无 CLI 入口）；3) 手工管道（E） | `docs/e2e-cli-loop.md`；EKB-042 实测 `generic-cli` issue `f1b0ec03…` |
| DSH（插件适配器） | DSH 0.2.0-rc.2 + `dsh-errkb` 0.0.0（本树） | **A** 插件钩子 | 是——常驻 system-prompt 提示 + post-execute 提示；已在 headless 会话日志中验证 | 用 `errkb run --` 包装 agent 命令（丢失宿主会话与工具事件），或手工 E | EKB-039 计划行、PR #66 |
| Codex CLI | 0.156.1 | **B** 包装器采集；**MCP** 八个工具负责查询/记录 | 否——`capability.json` 记录 `notice_before_diagnosis: false` | 解析 `Stop` 钩子的会话记录取失败工具项（已记录、未实现）；过渡：把一行日志交给包装器 | `connectors/codex/probe.md`（探针 2026-10-20T14:15:07Z）、PR #67 |
| Claude Code | 2.1.282 | **A** `PostToolUseFailure` 钩子；**B** 包装器兜模型 API 失败；**MCP** 八个工具 | 是——`SessionStart` 提示脚本 + MCP 工具在诊断前即可用 | 用 `errkb run --` 包装 `claude -p`（丢失 `tool_name`，仍有命令/退出码/文本） | `connectors/claude/probe.md`（探针 2026-10-20T15:55:00Z）、PR #68 |

模板类别（EKB-042）不是宿主：`connectors/templates/cli-log` 描述等级 B（等同
通用 CLI 行）、等级 C（仅日志意图，尾随尚无 CLI 入口）与等级 E（人选的错误文本）。
C 与 E **没有实测**——见下方缺口。

## 字段覆盖

列值是该宿主在总览表所列通道下的实测能力。B 类宿主（CLI、Codex）共用包装器
流水线；DSH 与 Claude 走钩子/插件路径，宿主自身能报的字段不同。

| `ErrorEvent v1` 字段 | 通用 CLI（B） | DSH（A） | Codex（B） | Claude Code（A） |
| --- | --- | --- | --- | --- |
| `schema_version` | ✅ 核心 | ✅ 核心 | ✅ 核心 | ✅ 核心 |
| `event_id` | ✅ | ✅ | ✅ | ✅ |
| `connector_id` | ✅ 参数 | ✅ 配置 | ✅ 参数 | ✅ 钩子配置中的连接器 id |
| `session_id` | ✅ 参数 | ✅ 宿主会话 | ✅ 参数 | ✅ 钩子载荷的 `session_id` |
| `observed_at` | ✅ | ✅ | ✅ | ✅ |
| `channel` | ✅ B | ✅ A | ✅ B | ✅ A |
| `category` | ✅ `command-exit` | ✅ `tool` / `command-exit` / `llm` / `agent` | ✅ `command-exit` | ✅ `tool`（API 失败走 B） |
| `severity` | 🟡 核心分类器 | 🟡 核心分类器 | 🟡 核心分类器 | 🟡 核心分类器 |
| `transient` | 🟡 核心分类器 | 🟡 核心分类器 | 🟡 核心分类器 | 🟡 核心分类器 |
| `headline` | 🟡 核心 | 🟡 核心 | 🟡 核心 | 🟡 核心 |
| `normalized_message` | 🟡 核心 | 🟡 核心 | 🟡 核心 | 🟡 核心 |
| `redacted_sample` | 🟡 核心脱敏 | 🟡 核心脱敏 | 🟡 核心脱敏 | 🟡 核心脱敏 |
| `error_code` | 🟡 从标题文本提取 | ✅ `llm` 的失败码；🟡 `tool` 文本提取 | 🟡 从标题文本提取 | ⬜（`capability.json` 声明缺失） |
| `command` | ✅ argv | ✅ `tool` 路径（`exec` 命令）；⬜ `llm`/`agent` | ✅ argv | ✅ `tool_input.command`（实测） |
| `exit_code` | ✅ 进程退出码 | ✅ `tool` 路径，从 `[exit code: N]` 标记解析；⬜ `llm`/`agent` | ✅ 进程退出码 | 🟡 从钩子错误文本解析（实测 `Exit code 2`） |
| `tool_name` | 🟡 被包装的可执行文件名（如 `codex.exe`），不是宿主工具名 | ✅ 宿主工具名；🟡 `agent/error` 上是 agent id | 🟡 被包装的可执行文件名 | ✅ 宿主工具名（`Bash`，实测） |
| `os` | ✅ 包装器摘要 | 🟡 核心默认（守护进程的 `runtime.GOOS`），非宿主上报 | ✅ 包装器摘要 | ⬜ |
| `shell` | ✅ `COMSPEC`/`SHELL` 文件名 | ⬜ | ✅ 包装器摘要 | ⬜ |
| `runtime_versions` | 🟡 仅 errkb 自身 Go 版本 | ⬜（适配器不发送） | 🟡 仅 errkb 自身 Go 版本 | ⬜ |
| `project_key` | ✅ 参数 | ✅ 配置（缺省 `default`） | ✅ 参数 | ✅ 由钩子 `cwd` 派生 |
| `fingerprint` | ✅ 核心 | ✅ 核心 | ✅ 核心 | ✅ 核心 |
| `evidence` | ✅ 包装器 | ✅ 适配器 | ✅ 适配器 | ✅ 适配器 |
| `confidence` | ✅ | ✅ | ✅ | ✅ |

说明：

- `channel` 与 `category` 是两条独立轴：通道是采集路径（§3.1），分类是核心分类器
  与指纹使用的捕获来源。
- B 通道的 `tool_name` 与 `runtime_versions` 如实反映自身含义：被包装的可执行文件
  与 errkb 的 Go 版本，不是宿主的内部工具名或宿主版本。
- Claude Code 的钩子载荷没有 `error_code`、`os`、`shell`、`runtime_versions`；
  `exit_code` 来自钩子收到的错误文本。

## 诊断前回传

"诊断前回传"指宿主能在再次失败之前把已知解法递给模型：

| 宿主 | 实测行为 |
| --- | --- |
| DSH | **是。** 插件注入常驻 system-prompt 提示（在解码的 headless 会话日志中验证），并通过 `post-execute`/`pre-step` 投递提示；模型还可经工具桥调用 `err_lookup`。 |
| Claude Code | **是。** `SessionStart` 钩子打印提示，八个 MCP 工具（`mcp__errkb__err_*`）在任何命令执行前即可用；已用本地 mock API 驱动工具调用实测。 |
| Codex CLI | **否。** 包装器只能在命令失败后打印解法；MCP 工具可手工/由 agent 查询，但没有失败前触发的钩子。 |
| 通用 CLI | **否。** 与 Codex 相同：只有包装器，失败后才看到解法。 |
| 模板 C/E | **否。** C 由 `errkb tail --file` 跟随日志，失败后才记录；E 是人选的错误文本。 |

## 降级方式与字段损失

| 当这条路径不可用 | 改用 | 相比主行的字段损失 |
| --- | --- | --- |
| Codex 钩子/插件不可用 | 用 `errkb run --` 包装 `codex exec`（真实验收路径） | `tool_name`（只剩被包装文件名）；宿主自身的重试结果；结构化宿主事件 |
| Codex 需要工具级失败 | 解析 `Stop` 钩子的会话记录（已记录、**未实现**） | 当前标 ❓；实现前采集停在包装器级别 |
| DSH 插件装不上 | 用 `errkb run --` 包装 agent 命令 | `session_id`（宿主会话）、`tool_name`、agent/tool 事件结构 |
| Claude 钩子不可用 | 用 `errkb run --` 包装 `claude -p` | `tool_name` 与诊断前回传路径 |
| 只有日志文件 | 取一行候选文本交给 `errkb run --`（C 意图，实测为 B 级采集） | `exit_code` 与结构化字段；指纹更弱（文本里的时间戳、pid、路径差异会漏合并） |

## 缺口与影响

| 缺口 | 宿主 | 影响 |
| --- | --- | --- |
| `shell`、`runtime_versions` 缺失 | DSH、Claude Code | `retrievals.environment_diff` 里的环境细节变薄；指纹与匹配质量不受影响（指纹只用分类 + 规范化文本）。 |
| `error_code` 缺失或文本派生 | Claude Code（缺失），CLI/Codex（文本派生），DSH `tool`（文本派生） | 跨宿主的精确错误码匹配变弱，召回退到规范化文本路径。 |
| `tool_name` 不是宿主工具名 | CLI、Codex | 工具重排（`tool_name` 权重 0.05）在这两类宿主上作用变小。 |
| DSH `tools/result` 路径未在真实会话中跑过 | DSH | 该采集路径有单测，但真实验收用的是 `agent/request-error`；真实 DSH 工具失败**未验证**。 |
| Codex `PostToolUse` 未测 | Codex | Codex 内部的隐藏工具失败对包装器不可见；Stop 会话记录回退方案只记录、未实现（**未验证**）。 |
| 模板 C 仅开发机实测 | 模板宿主 | `errkb tail --file` 跟随日志并经守护进程记录分类后的失败（`tests/e2e/tail_capture_test.go`）；干净机运行为 EKB-052 决定下的可选证据（**windows-acceptance CI 覆盖已安装生命周期**）。 |
| 模板 E 未单独验收 | 手工宿主 | 手工通道本质是"人选的文本走包装器"，没有独立验收运行（**未验证**）。 |
| B/C 通道的文本派生标题 | CLI、Codex、Claude 包装器 | 同一失败带不同时间戳、pid、临时路径时可能不合并；规范化能去已知噪声，但长尾差异仍在。这是 B、C 通道记录的代价。 |

## 测量来源

| 来源 | 覆盖内容 |
| --- | --- |
| `docs/e2e-cli-loop.md`（2026-10-19） | 通用 CLI 包装闭环：失败 → issue → 经核心 API 录入解法 → feedback；无模型调用。 |
| EKB-039 计划行 / PR #66 | DSH 0.2.0-rc.2 headless profile：`agent/request-error` 采集、会话提示、`err_lookup`/`err_stats` 回传、三处适配器契约修复。 |
| `connectors/codex/probe.md` + `connectors/codex/capability.json`（探针 2026-10-20T14:15:07Z） | Codex CLI 0.156.1：钩子/插件/MCP 界面、`Stop` 载荷、`PostToolUse` 未测、包装器闭环。 |
| `connectors/claude/probe.md` + `connectors/claude/capability.json`（探针 2026-10-20T15:55:00Z） | Claude Code 2.1.282：钩子 shell 规则、`SessionStart`/`PostToolUseFailure`/`Stop` 载荷实测、JSON 输出、MCP 注册、钩子闭环。 |
| `connectors/templates/cli-log/README.md` + 实测 `errkb doctor`（EKB-042） | 模板 B/C/E 等级、`generic-cli` 的 B 通道失败与 doctor 输出。 |
| `tests/e2e/tail_capture_test.go`（2026-10-22） | 模板 C：`errkb tail --file` 把 permission-denied 行经守护进程记录，连接器为 C 通道，重放复用同一 issue（`issues=1 events=2 hits=2`）。 |

## 未验证清单

- Codex `PostToolUse` 钩子载荷与工具级采集（本地 OSS 模型无工具调用；宿主模型配额在探针期间耗尽）。
- 真实 DSH 会话中的 `tools/result` 采集（仅单测）。
- 模板 C 的干净机端到端为可选证据（EKB-052 决定；开发机 e2e 已覆盖行为）。
- 模板 E 手工通道的独立验收。
- 头部所列版本之外的任何宿主版本。

在对应来源出现之前，格子只能保持"未验证"。这张矩阵就是 README 里"看起来支持"
声明的刹车（计划 §12）。
