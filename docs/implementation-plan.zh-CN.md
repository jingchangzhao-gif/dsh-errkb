# 错误知识库（Error Knowledge Base）实施计划

[English](implementation-plan.md)

本文件是项目从现有 DSH 插件升级为多宿主技术错误复用工具箱的唯一施工基线。实施者应从 `EKB-001` 开始，按照任务编号推进；一张任务卡只有在自动化检查和人工验收都通过后才能标记为完成。

## 1 项目结论和版本范围

最终产品不是一个绑定单一模型的插件，也不是一个试图读取模型内部推理过程的桌面监控器。最终产品由一个本地核心平台和多个轻量连接器组成：

```text
宿主 Hook 插件 CLI 日志 手动输入
              │
              ▼
       标准 ErrorEvent
              │
              ▼
脱敏 分类 标题提取 指纹 匹配 去重
              │
              ▼
       PostgreSQL 知识库
              │
        ┌─────┴─────┐
        ▼           ▼
 errkb.exe      MCP 本地 API
 搜索与管理      返回已知解法
```

版本一只处理可以观察和验证的技术错误：

- 环境、工具链和运行时错误；
- 依赖、版本、配置、认证、权限和网络错误；
- 编译、测试、代码运行和命令退出错误；
- 工具调用、插件、Hook、MCP 和协议错误。

版本一明确不包含：

- 自动判断需求理解错误和模型幻觉；
- 读取任何模型未公开的内部思考过程；
- 默认启用 OCR 或持续截屏；
- 自动执行知识库中的修复命令；
- 云端共享、团队权限和向量检索。

这些能力只进入本文件最后的后续候选区，不得阻塞版本一。

## 2 最终交付形态

版本一的用户可见产品是 Windows 安装程序和一个主程序 `errkb.exe`。同一个二进制通过子命令承担后台服务、CLI、MCP 和诊断功能，避免交付多个难以理解的程序：

```text
errkb.exe                    托盘和搜索界面
errkb.exe daemon             本地后台服务
errkb.exe run -- <command>   包装并观察 CLI 进程
errkb.exe search <query>     搜索知识库
errkb.exe mcp                MCP stdio 服务
errkb.exe doctor             环境和连接器诊断
```

各宿主连接器只负责两件事：把可观察的失败转换成 `ErrorEvent`，以及在宿主允许时查询并回传历史解法。分类、脱敏、存储、搜索、解法可信度和审计全部属于核心平台，不能在不同插件里重复实现。

由于 PostgreSQL 和 PL/pgSQL 是明确技术要求，版本一默认交付为安装器管理的本地应用，而不是宣称为真正的单文件便携程序。开发阶段先连接外部 PostgreSQL 16；发布前通过 `EKB-051` 决定并实现本地实例安装和生命周期管理。如果必须改成单文件便携版本，应先重新决定数据库技术，不能同时承诺强制 PostgreSQL 和零依赖便携 EXE。

本结论是 `EKB-051`（本地实例安装与生命周期）和 `EKB-050`（发布打包）的前提；改变本结论必须先修改本节，并通知相关卡。`EKB-045` 是搜索界面卡，不依赖本决定。

## 3 采集和复用策略

### 3.1 连接可靠性等级

| 等级 | 通道 | 用途 | 版本一策略 |
| --- | --- | --- | --- |
| A | Hook 插件 结构化事件 API | 获得错误码、命令、退出码、会话和工具结果 | 优先实现 |
| B | CLI 包装器和结构化日志 | 获得 stdout、stderr、退出码和进程信息 | MVP 必须实现 |
| C | 普通日志尾随 | 兼容不能被包装的进程 | MVP 必须实现 |
| D | Windows UI Automation | 读取主动暴露无障碍文本的桌面程序 | 版本一之后按宿主实现 |
| E | OCR 剪贴板 手动热键 | 无结构化接口时的降级 | OCR 后置，手动输入可保留 |

MCP 主要负责让宿主主动查询和记录知识，不能被当作被动捕获所有错误的机制。被动捕获应由 Hook、插件、CLI 包装器或日志通道完成。

### 3.2 版本一首批宿主

首批闭环按照以下顺序完成：

1. 通用 CLI，通过 `errkb run --` 覆盖任意可启动命令；
2. 现有 DSH 插件，重构为核心平台的连接器并保留断线降级；
3. Codex，先探测当时版本可用的 Hook、插件和 MCP 能力，再实现结构化连接器；
4. Claude Code，先探测 Hook、JSON 输出和 MCP 能力，再实现结构化连接器。

DeepSeek、Kimi、Qianwen 和其他宿主先使用通用 CLI、日志或手动通道。只有在存在稳定、可测试的官方接口时，才增加具名连接器。桌面窗口名称不是接口契约，不能仅靠窗口标题声称正式支持。

## 4 核心数据模型

不能把每次采集到的字符串都直接保存为一条错误。数据库至少分为以下实体：

| 实体 | 作用 |
| --- | --- |
| `connectors` | 宿主、版本、通道和能力状态 |
| `sessions` | 一次宿主或 CLI 会话 |
| `events` | 脱敏后的原始观察事件，只追加 |
| `issues` | 多个相同事件聚合后的稳定问题编号 |
| `occurrences` | 问题在特定环境中的每次出现 |
| `solutions` | 一个问题的候选解决方案 |
| `solution_versions` | 解法正文、前置条件、风险和版本历史 |
| `outcomes` | 解法在特定环境中成功、失败或未知 |
| `retrievals` | 一次搜索命中了什么以及为什么命中 |
| `notices` | 返回给宿主的内容和估算 token 数 |
| `audit_log` | 修改、导入、导出、删除和受控操作记录 |

`events`、`issues`、`solutions` 和 `outcomes` 必须分离。一次出现不是一个新问题，一个问题也不能只有一个永远正确的解法。

标准事件 `ErrorEvent v1` 至少包含：

```text
event_id connector_id session_id observed_at channel
category severity transient
headline normalized_message redacted_sample
error_code command exit_code tool_name
os shell runtime_versions project_key
fingerprint schema_version evidence confidence
```

CUE 负责定义和验证事件、配置以及 MCP 工具契约；Go 负责运行时逻辑；PL/pgSQL 负责数据库唯一性、幂等写入、计数和事务内状态变化。

## 5 检索和错误合并规则

搜索界面可以采用浏览器地址栏式交互，但底层不能只使用一条模糊文本查询。固定顺序如下：

1. 问题编号、错误码和精确指纹；
2. 规范化消息、命令和工具名的确定性过滤；
3. PostgreSQL `pg_trgm` 生成候选；
4. Go 使用 Jaccard、CJK 二元组、环境版本和宿主一致性重排；
5. 返回最多三个候选，并说明匹配原因。

近似匹配只能返回候选，不能自动把两个问题永久合并。自动复用解法至少要求精确指纹命中，或经过用户确认的近似关联。错误合并比漏匹配危险，因此标注语料的首要指标是零错误合并，其次才是召回率。

版本一不使用 embedding、模型分类和外部搜索服务。只有确定性检索在真实语料中无法达到目标时，才建立新的决策任务。

## 6 安全和隐私边界

- 所有文本必须在写数据库、写日志和写离线队列前脱敏；
- 捕获到的文本始终是数据，不能作为命令或系统指令解释；
- 默认使用 Windows Named Pipe，本地 HTTP 默认关闭；
- 如果启用回环 HTTP，必须使用随机 token，且只绑定 loopback；
- 密钥、Cookie、Authorization、个人路径和原始请求标识不得进入数据库；
- 原始样本、规范化文本和审计记录分别设置保留期限；
- 用户可以暂停全部采集，并按连接器、项目和时间删除数据；
- 解法自动执行在版本一中不存在；复制解法也必须显示来源、适用环境和风险；
- UIA 和 OCR 只能在显式允许列表中的程序上运行，OCR 图像不得落盘。

## 7 当前项目核查结果

本次核查读取了根目录、`.github`、`docs`、`seeds`、`src` 和 `tests` 中的全部项目文件，并实际运行了现有工具链。

| 项目 | 核查结果 |
| --- | --- |
| Git 状态 | 当前目录没有 `.git`，是普通源码副本，不能安全执行分支、提交和回滚 |
| TypeScript 源码 | `src` 中 13 个模块，已覆盖 DSH 插件的大部分完整闭环 |
| 自动化测试 | 15 个测试文件；本机实际发现 598 个测试 |
| 通过情况 | 596 个通过，2 个失败；不是 README 声明的全绿状态 |
| 通过的命令 | `pnpm install --frozen-lockfile`、`pnpm build`、`pnpm typecheck`、`pnpm lint`、`pnpm format:check` |
| 失败的命令 | `pnpm test` |
| 失败一 | Windows 实文件系统下 50 并发写入时，创建 `.lock` 返回 `EPERM`，当前实现只把 `EEXIST` 当作锁已存在 |
| 失败二 | `mtimeMs(<file>/child)` 在当前 Windows 行为中返回 `ENOENT`，实现将其映射为 `undefined`，测试却要求抛错 |
| 依赖解析 | 锁文件实际安装 `dsh-llm` 和 `dsh-tools` `0.1.5-rc.3`，README 中仍主要写 `rc.2` |
| 外部工具箱 | `cmd`、`internal`、`db`、`spec`、`packaging` 均不存在，尚未开始 |

因此当前项目可以被视为“经过大量单元测试的 DSH 原型”，不能被标记为“已验证可用的多宿主产品”。

### 7.1 工具链实测事实（EKB-002）

以下命令在本次核查中于本机实测，输出原文保留；后续卡片以本表为版本口径。

| 工具 | 实测命令 | 输出 |
| --- | --- | --- |
| Node.js | `node -v` | `v24.16.0` |
| pnpm | `pnpm -v` | `11.7.0` |
| Go | `go version` | `go version go1.26.4 windows/amd64` |
| PostgreSQL | `psql --version` | `psql (PostgreSQL) 16.15` |
| PostgreSQL 服务 | `Get-Service postgresql*` | `postgresql-x64-16` 已运行，启动类型 `Automatic` |
| CUE | `cue version` | `cue version v0.17.1`（CUE language v0.17.1，Go go1.26.4，CGO_ENABLED 1） |
| DSH 宿主 | `dsh --version` | 命令不存在：宿主是桌面应用，不提供 `dsh` CLI |
| 依赖实际安装 | `node_modules/@deepseek-ai/*/package.json` | `dsh-llm 0.1.5-rc.3`、`dsh-tools 0.1.5-rc.3`、`cordis 4.0.2`、`schemastery 3.18.2`；`@deepseek-ai/dsh` 未安装 |
| 冻结安装 | `pnpm install --frozen-lockfile` | `Already up to date. Done in 287ms using pnpm v11.7.0`，退出码 0 |
| 锁文件 | `Get-FileHash pnpm-lock.yaml -Algorithm SHA256` | `96BBD6FB51412A529F0BD1208EAF59BFC82453ECAB48C1DB74A91BD6B40F54DB` |

DSH 宿主版本没有可用命令来源：`@deepseek-ai/dsh` 不在依赖里，宿主应用也不暴露 CLI。唯一可核对的生态证据是 `~/.dsh/profiles/desktop/node_modules/dsh-ops/package.json` 的 peer `@deepseek-ai/dsh ^0.2.0-rc.2`，说明实际宿主是 0.2.x 世代；README 版本表写的 `0.1.5-rc.2` 是历史口径，见 7.2。

### 7.2 dsh-llm / dsh-tools rc.2 → rc.3 口径说明

- 涉及包：`@deepseek-ai/dsh-llm`、`@deepseek-ai/dsh-tools`。peer 声明仍是 `^0.1.5-rc.2`，该区间本身就覆盖 `0.1.5-rc.3`，因此不需要升/降级声明，也不需要改区间。
- 实际安装：`pnpm-lock.yaml` 解析为两包 `0.1.5-rc.3`，`node_modules` 实测一致（见 7.1）。
- README 声称：`README.md:347`、`README.zh-CN.md:336` 仍写 `0.1.5-rc.2`，语义是“本设计验证时的版本”，属于历史口径，不是本机安装值；本卡作用域限定为 `package.json`、`pnpm-lock.yaml` 和本计划，不改 README，差异以本节为准。
- 对 13 个 `src/` 模块的影响：只用到 `@deepseek-ai/dsh-llm` 的 `createUserMessage`、`UserMessage`、`MessageSourceMap`、`ContentBlock`，以及 `@deepseek-ai/dsh-tools` 的 `defineTool`、`ToolDefinition`、`ToolRunContext`，没有发现 API 删除或改名；`pnpm typecheck`、`pnpm build`、`pnpm test`（598 项通过）在 rc.3 上全部通过，判定为行为不变。
- 后续实现口径：平台核心按 Go 实现；DSH 连接器按锁文件的 `0.1.5-rc.3` 和宿主 0.2.x 实现；不为 rc.2 或 rc.3 任一侧维护兼容分支。README 版本表已由 EKB-055 文档同步更新为 rc.3 与宿主 0.2.x 口径。

## 8 现有文件处置表

状态含义：保留表示继续作为正式文件；扩展表示在原文件上增加能力；重构表示职责改变；降级表示不再作为权威实现；删除表示在指定任务中移除。

施工语言归属：Go（`cmd/`、`internal/`）由 `gofmt` 和 `go vet` 检查；SQL 与 PL/pgSQL（`db/migrations/`）由迁移检查和并发测试检查；CUE（`spec/`）由 `cue vet` 检查；TypeScript（`src/`、`tests/`）由 `pnpm` 的五项检查；PowerShell（`packaging/`）只做打包，不承载业务逻辑。`prettier` 继续只约束 TypeScript 和 Markdown，Go、SQL、CUE 的格式由各自工具负责。

新增文件不在本节登记（本节只处置已有文件）。每张卡新建或修改哪些文件、按什么顺序，统一列在 11.1 节；一张卡只动它声明的文件。

### 8.1 根目录和基础设施

| 文件 | 状态 | 处理 |
| --- | --- | --- |
| `.gitattributes` | 保留 | 继续统一 LF；将 Go、SQL、CUE 和 PowerShell 纳入相同规则 |
| `.gitignore` | 扩展 | 增加 Go 构建、临时数据库、安装器、CUE 生成物和本地配置忽略项 |
| `.prettierignore` | 保留 | 只约束 TypeScript 和 Markdown；不得负责 Go、SQL、CUE 格式 |
| `LICENSE` | 保留 | Go 核心、连接器和安装器继续使用 MIT；第三方二进制另建 notice |
| `package.json` | 保留 | 继续描述 DSH 连接器，不再描述整个工具箱运行时 |
| `pnpm-lock.yaml` | 保留 | DSH 连接器锁文件；修正文档中的真实依赖版本口径 |
| `pnpm-workspace.yaml` | 扩展 | 需要多个 TypeScript 连接器时才增加 workspace 包 |
| `tsconfig.json` | 保留 | 只服务 TypeScript 连接器和一致性语料生成器 |
| `tsdown.config.ts` | 保留 | 继续构建 DSH 连接器 |
| `vitest.config.ts` | 扩展 | 保留覆盖率门槛，新增一致性语料测试，不隐藏 Windows 失败 |
| `cordis.patch.yml` | 保留并重构 | 仍注册 DSH 连接器，后续增加核心服务地址和断线策略配置 |
| `README.md` | 重写 | 英文项目入口，描述平台加连接器，不再把旧设计文档作为真源（EKB-055 已完成） |
| `README.zh-CN.md` | 重写 | 中文项目入口，与英文逐节同步（EKB-055 已完成） |
| `README.toolbox.md` | 删除 | 内容被主 README 和本计划完全覆盖（已删除，EKB-055） |
| `README.toolbox.zh-CN.md` | 删除 | 与英文入口一同删除（已删除，EKB-055） |
| `.github/workflows/ci.yml` | 扩展 | 新增 Go、CUE、PostgreSQL 服务容器和 Windows 文件系统四个 job：Go job 跑 `gofmt -l`、`go vet`、`go test`；CUE job 跑 `cue vet ./spec/...`；数据库 job 在 PostgreSQL 16 服务容器内跑迁移和 50 并发测试；Windows 文件系统 job 在 `windows-latest` 上复现第 7 节的两个失败用例，防止回归；原有 TypeScript job 不变 |
| `.github/workflows/privacy-guard.yml` | 重构 | 规则由机器可读安全规则生成或验证，避免多处手工复制 |
| `.github/pull_request_template.md` | 重写 | 使用 `EKB-nnn`、阶段门槛和中英文同步检查 |

### 8.2 现有 TypeScript 源码

| 文件 | 状态 | 在新架构中的职责 | 新语言落点（文件 · 负责卡） |
| --- | --- | --- | --- |
| `src/signature.ts` | 保留 | DSH 离线降级与一致性参考；平台核心由 Go 实现 | `internal/core/normalize.go`、`internal/core/fingerprint.go`（EKB-013） |
| `src/capture.ts` | 重构 | 保留 DSH 载荷转换；通用分类迁移到 Go 核心 | `internal/capture/pipeline.go`；通用分类在 `internal/core/classify.go`（EKB-016、EKB-031、EKB-032） |
| `src/redact-patterns.ts` | 扩展后生成 | 安全规则迁入统一 schema，再生成 TypeScript、Go 和 CI 可用形式 | 唯一来源 `spec/redact.cue`，生成 `internal/redact/rules_gen.go`（EKB-014） |
| `src/redact.ts` | 保留 | DSH 离线降级仍使用；平台核心由 Go 实现 | `internal/redact/redact.go`（EKB-014） |
| `src/match.ts` | 降级 | 作为 DSH 离线匹配和一致性参考；权威搜索迁到 PostgreSQL 加 Go | `internal/core/tokenize.go`、`internal/core/jaccard.go`（EKB-015）；候选由 `db/migrations/` 的 `errkb_search()` 提供（EKB-022 至 EKB-024） |
| `src/inject.ts` | 保留 | 属于 DSH 连接器，继续负责该宿主的通知长度和频率 | —（只属于 DSH 连接器） |
| `src/resolve-detect.ts` | 重构 | 只产生 outcome 事件；可信度由核心数据库统一计算 | 可信度在 `db/migrations/0003_solutions.sql` 的 `errkb_trust()`（EKB-021） |
| `src/store.ts` | 降级 | 不再是权威存储；保留 Markdown 导入导出和断线队列转换 | `internal/database/`（EKB-019 至 EKB-026） |
| `src/state.ts` | 大幅缩减 | 只保存连接器本地健康状态和未发送队列，不再保存权威命中或可信度 | 连接器队列留在 TypeScript；守护进程状态在 `internal/service/daemon.go`（EKB-028）与 `internal/service/queue.go`（EKB-033） |
| `src/paths.ts` | 重构 | 只解析 DSH 连接器配置和离线队列路径 | —（只解析 DSH 路径） |
| `src/plugin.ts` | 重构 | 从完整知识库流水线改为 DSH 事件连接器和解法回传客户端 | 经 Named Pipe 调 `internal/service/pipe.go`（EKB-038） |
| `src/tools.ts` | 重构 | 五个 DSH 工具改为调用核心 API；断线时只提供明确降级行为 | 经 Named Pipe 调核心 API；服务端在 `internal/mcp/tools.go`（EKB-037、EKB-038） |
| `src/index.ts` | 扩展 | 注册 `agent/request-error`，增加核心服务配置并完成真实宿主验收 | —（EKB-039 只改连接器） |

### 8.3 测试和种子

| 文件 | 状态 | 处理 | 新语言落点（文件 · 负责卡） |
| --- | --- | --- | --- |
| `tests/signature.test.ts` | 保留 | 生成 Go 一致性语料并继续约束 DSH 降级实现 | 生成 `spec/corpus/normalize.json`、`spec/corpus/fingerprint.json`（EKB-009）；Go 侧 `internal/core/normalize_test.go`（EKB-013） |
| `tests/capture.test.ts` | 保留并拆分 | 通用案例进入跨语言语料，DSH 载荷案例留在连接器测试 | 生成 `spec/corpus/headline.json`（EKB-009）；Go 侧 `internal/capture/pipeline_test.go`（EKB-031、EKB-032） |
| `tests/redact.test.ts` | 保留并扩展 | 所有输出端共用同一敏感数据用例 | 生成 `spec/corpus/redact.json`（EKB-009）；Go 侧 `internal/redact/redact_test.go`（EKB-014） |
| `tests/redact-patterns.test.ts` | 重构 | 从比较两份手写正则改为验证生成结果 | `internal/redact/rules_gen_test.go`（EKB-014） |
| `tests/match.test.ts` | 保留并扩展 | 增加真实标注语料、零误合并门槛和 PostgreSQL 候选测试 | 生成 `spec/corpus/tokenize.json`、`spec/corpus/match.json`（EKB-009）；Go 侧 `internal/core/rerank_test.go`（EKB-017）、`internal/database/search_test.go`（EKB-024） |
| `tests/inject.test.ts` | 保留 | 继续约束 DSH 通知内容和硬上限 | 生成 `spec/corpus/notice.json`（EKB-009）；无 Go 对应 |
| `tests/resolve-detect.test.ts` | 重构 | 验证 outcome 事件，而不是连接器本地权威可信度 | 权威可信度测试在 `internal/database/outcome_test.go`（EKB-021） |
| `tests/store.test.ts` | 先修复后降级 | `EKB-003` 修复两项 Windows 失败；之后保留 Markdown 往返测试 | Markdown 往返对 `internal/database/markdown_test.go`（EKB-025） |
| `tests/state.test.ts` | 重写 | 只验证队列和连接器健康状态 | 守护进程状态在 `internal/service/daemon_test.go`（EKB-028）、`internal/service/queue_test.go`（EKB-033） |
| `tests/paths.test.ts` | 重写 | 只验证连接器配置和离线目录 | — |
| `tests/plugin.test.ts` | 扩展 | 增加 `agent/request-error`、核心服务断开和恢复测试 | 断线恢复在 `internal/service/pipe_test.go`（EKB-029、EKB-038） |
| `tests/injection.test.ts` | 保留并改接核心 API | 验证真实连接器调用路径和不重复计数 | `internal/mcp/tools_test.go`（EKB-037） |
| `tests/tools.test.ts` | 重构 | 验证 DSH 工具到核心 API 的请求和错误映射 | `internal/mcp/tools_test.go`（EKB-037、EKB-038） |
| `tests/settings.test.ts` | 扩展 | 增加服务地址、Named Pipe、超时和离线策略配置 | 配置契约校验在 `spec/config.cue` 与 `internal/contract/config_test.go`（EKB-007） |
| `tests/seeds.test.ts` | 保留 | 种子同时能导入 PostgreSQL并保持脱敏 | `internal/database/seed_test.go`（EKB-025） |
| `tests/.gitkeep` | 删除 | 测试目录已经非空，不再需要 | — |
| `seeds/ERRORS.seed.md` | 保留并扩展 | 作为导入兼容样本；另生成结构化 seed 数据 | 生成 `seeds/*.json`；导入测试（EKB-025） |

### 8.4 文档目录

| 原文件 | 状态 | 替代物 |
| --- | --- | --- |
| `docs/设计说明书.md` | 已删除 | 本文件 |
| `docs/design-spec.md` | 已删除 | `implementation-plan.md` |
| `docs/discussions.zh-CN.md` | 已删除 | 已决定内容并入本文件，未决定内容转成任务卡 |
| `docs/discussions.md` | 已删除 | 英文计划对应章节 |
| `docs/upgrade-tasks.zh-CN.md` | 已删除 | 本文件的连续任务序列 |
| `docs/upgrade-tasks.md` | 已删除 | 英文计划的连续任务序列 |

## 9 目标工程结构

```text
<repo-root>/
├── cmd/errkb/                    # Go 主程序和子命令
├── internal/
│   ├── contract/                 # CUE 生成后的 Go 契约
│   ├── core/                     # 标题 脱敏 指纹 匹配重排
│   ├── capture/                  # run 日志 手动及连接器管线
│   ├── database/                 # pgx 仓库和迁移器
│   ├── service/                  # daemon Named Pipe 和健康检查
│   ├── mcp/                      # MCP stdio 服务
│   ├── ui/                       # 托盘 omnibox 详情页和通知
│   └── audit/                    # 审计和指标
├── db/migrations/                # SQL 和 PL/pgSQL
├── spec/
│   ├── error-event.cue
│   ├── config.cue
│   ├── mcp-tools.cue
│   └── corpus/                   # 脱敏后的一致性和检索语料
├── connectors/
│   ├── codex/
│   └── claude/
├── src/                          # 现有 DSH TypeScript 连接器
├── tests/                        # DSH 和跨语言语料生成测试
├── packaging/windows/            # 安装 升级 卸载和发布脚本
├── docs/
│   ├── implementation-plan.md
│   └── implementation-plan.zh-CN.md
└── seeds/
```

## 10 任务状态和工作方式

任务状态只能使用：

- `☐ 未开始`
- `◐ 进行中`
- `☑ 已验收`
- `⚠ 阻塞`

每张卡使用以下固定流程：

1. 确认所有前置卡已经验收；
2. 创建 `task/EKB-nnn-short-name` 分支；
3. 在本文件把卡改成进行中；
4. 先写或更新测试，再修改实现；
5. 只修改任务卡声明的作用域；
6. 运行该卡验收和公共质量门；
7. 进行一次真实场景演示；
8. 同步中英文文档；
9. 把状态改为已验收，并记录实测命令；
10. 使用 `type: summary (EKB-nnn)` 提交。

若一张卡连续两次无法通过验收，应拆小任务或移出版本一，不能降低门槛或直接勾选。

## 11 按顺序实施的任务

每张卡新建或修改的文件，按卡号顺序列在 11.1 节；那张表就是施工时的操作顺序。

### P0 建立可信基线

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☐ | EKB-001 | 建立真实 Git 工作区 | 无 | 从上游 `main` 克隆，复制本计划，记录 HEAD；`git status` 可用且基线可回滚 |
| ☑ | EKB-002 | 固定工具链和依赖事实 | 001 | 已完成：见 7.1 与 7.2；`node -v`、`pnpm -v`、`go version`、`psql --version`、`cue version` 均已实测；rc.2 与 rc.3 差异已书面说明；`pnpm install --frozen-lockfile` 退出码 0 |
| ☑ | EKB-003 | 修复现有 Windows 测试基线 | 001 | 已完成：PR #17 合入 `main`；`EPERM`/`EBUSY` 视为锁竞争，`mtimeMs` 仅当父路径是目录才返回 `undefined`；`pnpm test` 15 文件 598 项全绿，`store.ts` 覆盖率 99.32% 语句 / 97.17% 分支；两处均为实现错，测试未改 |
| ☑ | EKB-004 | 确认产品和数据库分发决定 | 001 | 已完成：§2 书面定稿——发布物不承诺零依赖（单一答案：否）；开发期外部 PostgreSQL 16，发布期由 `EKB-051` 托管本地实例；强制 PostgreSQL 与单文件便携 EXE 不可同时承诺；下游为 `EKB-051` 与 `EKB-050` |
| ☑ | EKB-005 | 建立新目录和双工具链 CI | 002-004 | 已完成：Go 1.26.4 空骨架（`cmd/errkb`、8 个 `internal/*/doc.go`、3 个 `spec/*.cue`、`db/migrations/0001_init.sql`、`packaging/windows/build.ps1`）；CI 新增 go / cue / database / windows-filesystem 四个 job，PR #21 六个检查全绿；本地 `gofmt -l` 无输出，`go vet`、`go test`、`cue vet` 均退出 0，`build.ps1` 产出 `errkb 0.1.0-skeleton windows/amd64`；数据库 job 在 PostgreSQL 16 容器内按文件名顺序执行迁移并跑 50 个并发会话 |

P0 门槛：仓库可回滚；现有测试全绿；新的空骨架全绿；数据库分发方式已有书面决定。

### P1 冻结契约和一致性语料

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-006 | 定义 `ErrorEvent v1` | 005 | 已完成：`spec/error-event.cue` 闭合契约（`channel` 取 §3.1 的 A–E，`category` 复用 `src/capture.ts` 的四个历史取值，`#NoSensitive` 最小脱敏约束），`internal/contract/error_event.go` 校验入口拒绝未知字段与未脱敏文本，测试含 3 条合法事件、17 个必填字段缺失各一例、4 类敏感样本；`cue vet`、`go vet`、`go test` 全绿 |
| ☑ | EKB-007 | 定义配置和连接器能力契约 | 006 | 已完成：`spec/config.cue` 与 `spec/connector-capability.cue` 定义内部校验（用户仍写 YAML/JSON），`internal/contract/config.go` 用 `gopkg.in/yaml.v3` 解析并拒绝未知键；安全默认值（Named Pipe 开、回环 HTTP 关、HTTP 必须有 ≥32 字符 token 且只能绑回环）、采集暂停（按连接器/项目）、三段独立保留期、超时与离线；能力契约用通道 A–E + 缺失字段表达降级，宿主名是开放字符串；6 个新测试全绿，`cue vet`、`go vet`、`gofmt` 全绿。偏差：为解析用户实际编辑的 YAML，`go.mod`/`go.sum` 新增 `gopkg.in/yaml.v3`（超出本卡四文件范围，已在 PR 说明） |
| ☑ | EKB-008 | 定义 MCP 和本地 API 契约 | 006 | 已完成：`spec/mcp-tools.cue` 六个操作（lookup/search/record/feedback/list/stats）各自带 `schema_version`，候选带匹配原因与 `approximate` 标志且上限 3；`spec/local-api.cue` 复用同一批负载定义做 Named Pipe 信封（信封版本、`request_id`、`result`/`error` 二选一、`#ErrorCode` 闭集）；`internal/contract/tools.go` 与 `local_api.go` 提供同一组 `Validate()` 与严格 JSON 解码入口；`record` 复用 `ErrorEvent` 校验，`search` 与 `lookup` 语义分离，`feedback` 只能记 outcome；`cue vet`、`go vet`、`go test`、`gofmt` 全绿，23 例探针通过（探针已删除，因本卡文件清单不含测试文件） |
| ☑ | EKB-009 | 从现有测试生成一致性语料 | 006 | 已完成：`tests/gen-corpus.ts` 生成七份语料共 133 例（normalize 57、fingerprint 19、headline 14、redact 20、tokenize 6、match 6、notice 11）；连续生成 SHA-256 一致，`--check` 无改动返回 0、改动语料返回 1；六个测试文件新增 133 条读语料断言（原断言未删），`pnpm test` 732 项全绿；隐私守卫与验收 5 扫描 0 命中 |
| ☑ | EKB-010 | 建立真实标注检索语料 | 009 | 已完成：`spec/corpus/labelled/` 三集合 29 例（same-error 10、distinct-lookalike 12、cross-language 7，其中 1 例 uncertain），每例含成对输入、判定依据、基线测量、确认人与确认日期（jingchangzhao-gif / 2026-10-19）；三类易混各 4 例；脱敏与验收 5 扫描 0 命中；TS 基线在 12 例易混中 6 例会错误自动合并，在 16 例同错中 12 例命中、4 例漏检 |
| ☑ | EKB-011 | 建立 schema 兼容规则 | 006-010 | 已完成：`spec/compat.cue` 登记五个契约（error_event/config/connector_capability/mcp_tools/local_api）的字段、必填、枚举、未知字段策略、版本锚点和解码器，并定义 R1–R17 规则（R17：EKB-035 回填，新增定义及其内部必填字段=兼容）；新增 `compat` CI job：先自测 17 条规则，再导出 base 与 HEAD 清单并分类差异，破坏性或语义变更必须把 schema_version 从 vN 提升到严格更大的值；实测删除 `command` 字段且不升版本时输出 `violation [R9] ...` 并以 1 退出，同一删除升到 v2 后只记录 `[R3] ... (breaking)` 并以 0 退出；`cue vet ./spec/...` 通过；四份严格解码器对应策略为 reject，ErrorEvent 持久行的宽松读取留给 EKB-053 |

P1 门槛：TypeScript 与未来 Go 实现可以消费同一份确定性语料，所有外部输入都有版本化契约。

### P2 实现 Go 确定性核心

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-012 | 实现标题提取和安全错误文本 | 009 | 已完成：`internal/core/headline.go` 按语料 14 例实现 traceback 末行、错误码优先、末行回退；只 import regexp/strings/unicode，零 I/O；二次调用结果一致；ANSI 剥离与 200 rune 上限（199 字符 + `…`）与语料一致；命令前缀组合在 `capture()` 中，语料无此例，留到 EKB-016/031；`go test ./internal/core/ -run Headline`、`go vet`、`gofmt -l` 全绿，`spec/corpus/headline.json` 未改动 |
| ☑ | EKB-013 | 实现规范化和指纹 | 009 | 已完成：`internal/core/normalize.go` 与 headline 共用 ANSI 规则，按语料 57 例实现 UUID/哈希/时间/时长/PID/端口/行列/临时目录/绝对路径折叠与首尾标点裁剪；`internal/core/fingerprint.go` 只做 `sha256(category + "\x00" + normalized)[:12]`，与归一化分离；两侧生产代码零 I/O；Go 57 + 19 例与 TS 侧 106 项断言同源于语料；语料未改（normalize.json `3237EF88…`，fingerprint.json `2B8383AA…`）；`gofmt` / `go vet` 干净 |
| ☑ | EKB-014 | 实现统一脱敏引擎 | 009、011 | 已完成：`spec/redact.cue` 是唯一来源（15 条文本规则 + 3 条 home 前缀 Go 变体 + 3 条 guard 备选），`tests/gen-redact-rules.ts` 生成 `internal/redact/rules_gen.go`、`src/redact-patterns.ts` 与 `privacy-guard.yml` 的 PATTERN 块；三件产物共享同一 SHA-256 源摘要，`--check` 零差异、篡改后非零；`internal/redact/redact.go` 纯函数零 I/O，public 先折叠绝对路径、private 先折叠 home 前缀，再按序应用规则；Go 20 例语料与 TS 一致，每条规则用自己的 example 命中；`redact-patterns.test.ts` 改为校验生成结果；偏差：导出 `core.CollapseAbsolutePaths` 包装、`spec/compat.cue` 增加 `redact` 契约（tolerate）；写盘点尚未接入（EKB-019/031 在写入前调用） |
| ☑ | EKB-015 | 实现 token 化和 Jaccard | 009 | 已完成：`internal/core/tokenize.go` 手动扫描而非单一正则（RE2 无 lookahead，改用「占位符 → CJK 连续段 → 其余字母数字下划线」的优先级切分）：占位符整体成 token、CJK 按 rune 取二元组（单字保留 unigram）、Latin 词保持原样；返回去重且 `sort.Strings` 排序的切片，两次调用逐字节一致；`internal/core/jaccard.go` 只返回 0..1 的数值（不返回合并布尔），空集按 TS 语义返回 0；语料 `tokenize.json` 6 例全绿，`match.json` 的 0.71/0.72/0.73 三点以 1e-9 容差断言；两文件零 I/O；`gofmt` / `go vet` 干净 |
| ☑ | EKB-016 | 实现分类和瞬时噪声规则 | 012-015 | 已完成：`internal/core/classify.go` 输出 §1 四类（environment / dependency / build / tool-call）+ 显式 `unknown`，类别优先级 tool-call → environment → dependency → build → unknown；瞬时判定优先级「永久码 → 瞬时码 → 永久文本 → 瞬时文本 → 默认永久」，永久码 AUTH/QUOTA/INVALID_REQUEST/CONTEXT_OVERFLOW/NO_ADAPTER/UNKNOWN 压过消息里的 timeout/rate limit，`ENOTFOUND` 等「看着像瞬时实为永久」有边界用例；severity 域为 warning（瞬时）/ error（永久）；分类器无阈值、无计数、无「是否建 issue」，不把通道等级 A–E 当类别；`go test ./internal/core/ -run Classify` 全绿（类别 5 类各多例、优先级、瞬时边界、交叉组合、确定性），两文件零 I/O（仅 regexp/strings）；偏差：`classify.go` 的 §1 类别域与 `spec/error-event.cue` 里 EKB-006 定义的 `#Category`（tool/command-exit/llm/agent，指纹输入）尚未统一，本卡作用域只含分类器与测试，两者统一及连接器映射留给 EKB-031/032 |
| ☑ | EKB-017 | 实现候选重排和匹配解释 | 015-016 | 已完成：`internal/core/rerank.go` 纯函数 `Rerank(query, candidates)`，权重集中声明（token 相似度 0.55 / 错误码 0.15 / 命令 0.10 / 工具名 0.05 / 环境 0.15；CJK 二元组已包含在 `Tokenize` 里，不另设权重），硬上限 3 条；返回值只有 `IssueID`/`Score`/`MatchedFields`/`Approximate`/`EnvironmentDiff`，无合并结论字段（用反射断言）；匹配原因枚举 `fingerprint`/`error_code`/`command`/`token_similarity`/`environment`；环境差异按 os/shell/connector/runtime_versions.<key> 逐项列出而不是只降分；tie-break 规则：分数降序 → 精确优先 → `IssueID` 升序，输入乱序结果一致；`distinct-lookalike` 12 对全部冒烟通过且无一标为精确命中。关键决定：精确 = 指纹命中 **且** 指纹未覆盖的字段（错误码/命令/工具名）互不矛盾——同一条消息出自不同命令是不同问题，这正是标注语料里 005–008 那几对共享指纹的陷阱；`go test ./internal/core/ -run Rerank` 全绿，两文件零 I/O（仅 sort/strings） |
| ☑ | EKB-018 | 完成跨语言差分测试 | 012-017 | 已完成：`internal/core/diff_test.go`（外部测试包 `core_test`，因为 `internal/redact` 依赖 `internal/core`，同包内无法互相导入）逐字段比对七份语料，失败信息带语料名/`case_id`/字段名/Go 值/TS 值；TS 侧判据是 EKB-009 由 TS 实现生成的语料期望值（生成器 `--check` 保证同步）。登记差异 3 条并逐条锁定：`match` 的 `matched`/`id`/`via`/`approximate`（`match.ts` 已降级，Go 侧只做 token 化和相似度，排序是 EKB-017 的 `Rerank`）、`match.similarity`（TS 按路线定义：精确=1、code 兜底=0、fuzzy=Jaccard；Go 只有 Jaccard，精确命中路线因此不同）、`notice`（`inject.ts` 属于 DSH 连接器，无 Go 对应）。`spec/corpus/divergences.md` 与测试内的登记表双向同步，任一侧改动而另一侧未改即失败；源文件扫描禁止 `internal/core` 出现 `Notice` 实现或 `Match` 函数，出现即提示更新登记。P2 收口证据：零 I/O 导入扫描（`internal/core` 与 `internal/redact` 的 13 个非测试文件无 `os`/`net`/`net/http`/`os/exec`/`io/ioutil`/`math/rand`/`crypto/rand`）通过；`go test ./internal/core/ -run Diff` 全绿；标注语料 12 对冒烟仍无精确命中；`pnpm test` 15 文件 732 测试全绿 |

P2 门槛：核心包完全无 I/O；不调用模型、不访问网络；跨语言语料全绿且不存在未记录偏差。

### P3 建立 PostgreSQL 权威知识库

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-019 | 建立迁移框架和 schema v1 | 005、011 | 已完成：`0001_init.sql` 实装第 4 节全部 11 张表（事件表不含 `issue_id`，`audit_log` 无外键，样本、规范化文本、审计各有独立保留期列，审计字段齐全）；`internal/database/migrate.go` 用 `schema_migrations` 记录版本+sha256 校验和，事务前取 PostgreSQL advisory lock，每个文件一个事务、成功后写 ledger，已应用文件被改动或缺失报错；0001 从未落到持久库（本地实例 0 张表、CI 每次新容器），故就地填充并在 `db/migrations/README.md` 记录证据；本地 PostgreSQL 16.15 六个用例实测全绿（新库 11 表、重复无副作用、篡改报校验和、失败回滚、并发只应用一次、结构断言），`psql -v ON_ERROR_STOP=1` 路径也实测通过；偏差：新增 `github.com/jackc/pgx/v5` 依赖（`go.mod`/`go.sum`），`go` CI job 无数据库服务故迁移测试跳过、由本地实测覆盖 |
| ☑ | EKB-020 | 实现 PL/pgSQL 幂等记录 | 019 | 已完成：`errkb_record_event()` 在调用方事务内用唯一约束 + `ON CONFLICT` 记录（没有 Go 侧“先查后写”，因此也不需要 advisory lock）：事件按 `event_id` 只插一次且从不改写，重放若指纹不同报 `23505`；`issues` 按精确 `(fingerprint, project_key)` upsert 并维护 hits、first_seen、last_seen；每次调用都插入一条 `occurrences`（同时删除 EKB-019 的 `(issue_id, event_id)` 唯一约束，否则重放会被折叠成一条）；函数不计算信任分（EKB-021 的 `errkb_trust()` 负责）。本地 PostgreSQL 16.15 实测 `go test ./internal/database/ -v` 全绿：同一 `event_id` 重放 10 次得到 1 event、1 issue、10 occurrences、hits=10，且已存 headline 不被覆盖；50 并发（pgxpool、每 goroutine 独立连接、同一精确指纹）得到 1 个 issue、50 occurrences、hits=50；异指纹重放被拒；两个不同消息得到两个 issue；事务内强制失败后 events/issues/occurrences 一起回滚；未知 connector 返回可读错误。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿。偏差：为适配第二个迁移修改了 `internal/database/migrate_test.go` 中并发/重复迁移的断言（按 `LoadMigrations` 实际数量，而不是硬编码 1），并在函数头加 `#variable_conflict use_column` 以区分 OUT 参数与同名列 |
| ☑ | EKB-021 | 实现解法版本和 outcome | 019 | 已完成：`0003_solutions.sql` 把 `solutions` 状态机改为 candidate/verified/deprecated/blocked（触发 `errkb_solution_status_transition` 只放行 candidate→verified/deprecated/blocked、verified→deprecated/blocked、blocked→deprecated，deprecated 为终态，其余迁移报 23514；0001 的 accepted→verified、rejected/superseded→deprecated 在换约束前先折叠）；`solution_versions` 补 `effective_at` 与 `applies_to`（§6 的适用环境）并保留正文/前置条件/风险/来源；`outcomes` 补 environment 的 GIN 索引；新增确定性 `errkb_env_matches()`（os/shell/connector 相等、runtime_versions 逐键包含、空环境匹配全部）与权威 `errkb_trust(solution_id, environment)`：`(successes+2)/(successes+failures+4)` 的 Laplace 平滑，零样本=0.5、一次成功=0.6、五次成功=7/9，`unknown` 计入样本但不进分母，同输入同输出、无时间/随机依赖；库里没有任何 trust 列，客户端没有写入路径。`internal/database/solution.go` 提供 CreateSolution、AddSolutionVersion（版本号在语句内取 max+1，历史不可覆盖）、TransitionSolution、RecordOutcome（空环境自动取关联 occurrence 的 environment）、SolutionTrust。本地 PostgreSQL 16.15 实测 `go test ./internal/database/ -v` 全绿：状态机合法链通过、deprecated→verified 与 blocked→verified 被库拒绝、枚举外值撞 CHECK、三版历史完整且第 1 版正文未被覆盖、windows 成功与 linux 失败分环境可查且另一 runtime 版本零样本、unknown 落库且样本=1、0.5/0.6/7/9 确定性且失败拉低、information_schema 中没有 trust 列。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿 |
| ☑ | EKB-022 | 实现精确检索 | 019-021 | 已完成：`db/migrations/0004_exact_search.sql` 的 `errkb_search()` 只接受一个选择子（编号、12 位指纹、错误码、规范化消息、命令或工具名，可带 `project_key`），其余参数报 22023，查无结果返回空集；五种维度的等值谓词分别命中 `issues_pkey`、`issues_fingerprint_project_key_key`、`events_error_code_idx`、`events_command_idx`、`events_tool_name_idx`、`events_normalized_message_idx`，事件维度经 `occurrences_event_idx` 与 `issues_pkey` 回到 issue；匹配语义为整值等值且区分大小写，ID 与指纹先校验格式再等值比较，不存在前缀、`LIKE` 或 `pg_trgm` 路径；行序固定为 hits desc / last_seen desc / id asc。`internal/database/search.go` 只做调用封装，`search_test.go` 覆盖五维命中与未命中、前缀拒绝、空集与非法参数分离、项目过滤、limit 截断、重复调用同序，以及灌入 3000 行 issue/event 后六个维度 `EXPLAIN` 全部走 Index Scan 且无 Seq Scan（`issue id` 为 Index Only Scan）。本地 PostgreSQL 16.15 全绿 |
| ☑ | EKB-023 | 实现 `pg_trgm` 候选检索 | 019 | 已完成：`db/migrations/0005_trigram.sql` 把 `pg_trgm` 当作可选扩展，固定装到 `public`，仅在扩展可用时创建三个 GIN trigram 索引（`events_normalized_message_trgm_idx`、`events_command_trgm_idx`、`events_tool_name_trgm_idx`），并提供 `errkb_trigram_available()`；`errkb_candidates(query, project, threshold, limit, message_weight, command_weight, tool_name_weight)` 生成加权候选：阈值必须大于 0 且不超过 1（0 会退化成全表扫描），相似度用 `CASE WHEN <字段> % $1 THEN similarity(...) ELSE 0 END` 保证每字段只算一次，命中的字段进 `match_fields`，权重为 0 的字段不能单独产生候选，候选池在 join occurrences/issues 之前就硬性截到 500，之后按 issue 去重再取 `p_limit`；返回 `similarity`/`score`/`approximate`，没有任何合并结论字段。扩展不可用时走受限降级：只做三字段整值等值、同一 `p_limit` 上限、`degraded = true` 且 `similarity`/`score` 为 NULL，不会静默返回空集。`internal/database/candidates.go` 是类型化封装（`CandidateQuery`/`Candidate`/`CandidateSet`，可空相似度用 `pgtype.Float8`，SQLSTATE 22023 映射为可读错误）。本地 PostgreSQL 16.15 实测 `go test ./internal/database/ -run FuzzyCandidates -v` 全绿：相关 issue 命中且 `match_fields` 含 `normalized_message`、无关项不出现；反射断言 `Candidate` 没有合并字段；权重外置（命令权重置 0 后“仅命令命中”的候选消失）；limit 截断；7 个非法参数分支；事务内 `drop extension pg_trgm cascade` 后降级返回等值命中并标记 `degraded`，回滚后扩展恢复；6000 行数据上 `EXPLAIN` 命中 `Bitmap Index Scan` 与 `_trgm_idx`。基准（6000 行，`ResetTimer` 后）：选择性查询 2.34 ms/op，命令密集最坏情况 82.3 ms/op。GIN/GiST 实测（3000 行同命令模式）：单列热查询 GiST 1.2–1.3 ms 快于 GIN 8.9–12.8 ms，但完整加权函数两者相当（GIN 53–62 ms、GiST 47–57 ms），且 GiST 的 message 索引在 6001 行上已 1.67 MB，整体没有“明显更好”，按计划保留 GIN 并记录数据。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿。偏差：扩展固定 `schema public`、测试 `search_path` 改为 `<schema>, public`（`migrate_test.go`/`record_test.go`/`candidates_test.go`，否则受限 search_path 下 `gin_trgm_ops` 不可见）；函数内 `SET enable_seqscan = off` 作为作用域内提示（23k 行探针上 221 ms vs 53 ms），留给 EKB-027 的 10 万行门槛复评；基准前显式 `ANALYZE` |
| ☑ | EKB-024 | 接入 Go 重排和环境过滤 | 017、022-023 | 已完成：`internal/database/rerank.go` 按 §5 顺序串联五段——编号精确命中 → 指纹精确命中 + 确定性过滤（错误码、命令、工具名）→ `pg_trgm` 候选（EKB-023）→ 纯 Go 重排 `core.Rerank`（EKB-017）→ 截断 top 3 并落库；`internal/core/rerank.go` 未修改，SQL 全部留在数据库层。精确指纹命中置顶（score 1、`approximate=false`），近似项一律 `approximate=true`，`AutoReusable()` 只有在非近似且命中字段含 `fingerprint` 时才为真，因此近似候选不可能自动复用。候选环境取最新 occurrence 的 `environment`（os/shell/runtime_versions），与查询环境不一致时作为 `environment_diff` 返回而不是静默降分。每次检索把 top3 的 `candidate_rank`/`score`/`match_fields`/`approximate`/`environment_diff` 写入 `retrievals`，写失败则检索失败（不丢审计）。测试（`rerank_test.go`，本地 PostgreSQL 16.15 全绿）：`distinct-lookalike` 12 对零错误自动合并（005–008 消息与指纹相同但命令不同，靠命令矛盾判为近似）；`same-error` 10/10 命中（9 对指纹相同为精确命中，`same-error-009` 作为召回缺口记录，没有为提升召回而下调阈值）；`cross-language` 7 例全部经共享错误码检出；4 个相关 issue 时严格截断为 3；近似项不可自动复用、精确项可复用；`retrievals` 行数等于返回候选数且 rank 1..3、字段完整；环境差异可见 `os` 与 `runtime_versions.node`；空请求报错。架构边界：`TestDiffNoIOImports` 仍通过，`internal/core` 零 I/O。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿 |
| ☑ | EKB-025 | 实现 Markdown 导入导出 | 019-024 | 已完成：`internal/database/markdown.go` 实现旧格式的解析与渲染。导入端 `ParseMarkdown` 认 `## E-\d+ · 标题`、`<!-- errkb: sig= cat= code= first= device= proj= -->` 元数据注释（值按 `[%\s>]` 百分号编解码）、中英字段标签、` ```text` 围栏取原始信息、`- Fix:`/`- Notes:` 缩进续行与 `<!-- errkb-history: vN -->` 历史标记，遇到冲突标记、缺元数据、未知状态、重复编号、未闭合围栏直接报错；指纹按 `core.Signature(category, core.Normalize(raw))` 复算，与种子文件的 `sig` 逐字节一致（`88fe8fb1a189`/`bcf6cca60510`/`3a12e3627689`）。导入在一个事务里直接写 issues/events/occurrences/solutions/solution_versions（导入是数据交换不是新观测，不走 `errkb_record_event()`），事件编号由指纹+项目推导成 UUID（重复导入命中同一条 occurrence 并更新，不产生重复）；所有文本写库前先过 `redact`。旧三态映射显式实现：issue `open/fixed/wontfix`→`open/resolved/wontfix`（也认 `misjudged`），解法状态 `open→candidate`、`fixed→verified`、`wontfix→blocked`，回写 `candidate→open`、`verified→fixed`、`blocked→wontfix`、`deprecated→open`。多版本导出策略显式：最新版正文作为可见 Fix，旧版本以 `<!-- errkb-history: vN -->` 纯文本追加，TS 解析器可原样读取；`Notes` 存为最新版本的 `risks` 并原样导出，`preconditions` 旧格式无槽位、不导出。导出按 `created_at, fingerprint, project_key` 排序（同一事务创建的 `created_at` 相同，`id` 随机不可作次序），重新编号 `E-0001…`。`seeds/ERRORS.seed.md` 只读，结构化产物 `seeds/ERRORS.seed.json`（v1，3 条）由 `ExportSeedJSON` 生成，`ERRKB_WRITE_SEED_JSON=1` 重写。往返可由现有 `src/store.ts` 解析：测试用 tsdown 打包一个导入 `src/store.ts` 的探针并 node 运行，缺 pnpm/node 时跳过（CI 的 go job 无 Node）。本地 PostgreSQL 16.15 实测 `go test ./internal/database/` 全绿：种子 3 条解析/导入/状态；同一文档二次导入零新增版本；导出再导入到空库字节级一致；手工改触发与 Notes 后重导入手改生效；三态往返；倒序状态迁移被库拒绝；写库前脱敏（events/issues/versions 无明文密钥、出现 `<secret>`）；device/project/hits 往返；无 Markdown 读权威路径。隐私守卫误报修正：JSON 与 Go 字符串里“冒号+转义换行”会被 Windows 路径规则命中，导出时把 `:` 与换行都写成 `\u003a\u000a`，测试夹具改为 `[]string` 行拼接。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 22.2 s），全树隐私扫描 0 命中。偏差：occurrence 改为先查后更新（0002 已去掉 `(issue_id,event_id)` 唯一约束）；导出次序用 `created_at, fingerprint, project_key` 保证跨库确定性 |
| ☑ | EKB-026 | 建立备份 恢复 保留和删除 | 019 | 已完成：`db/migrations/0006_retention.sql` 补保留与删除所需索引，并用数据库强制“审计不可被普通删除”：`audit_log` 上的 BEFORE DELETE 触发器除 retention job（会话内 `errkb.audit_retention=on`）外一律 55000，BEFORE TRUNCATE 同样拒绝；`errkb_purge_retention(sample_days, normalized_days, audit_days, now)` 三类保留期彼此独立——超过样本期的样本就地清空（只清仍在规范化窗口内的行），超过规范化期的事件整行删除，超过审计期的审计行删除，每一步都写入一条 `retention.purge` 审计（含三类计数），天数为负报 22023；`errkb_delete_preview(project, connector, before)` 先预览将被删除的八类行数（强制至少一个过滤条件，审计恒为 0），`errkb_delete_scope(...)` 在事务内按 outcomes→retrievals→notices→occurrences→solution_versions→solutions→events→issues 顺序真删派生数据，并写一条 `delete.scope` 审计（范围、计数、操作者、时间），审计行不随业务数据删除。`internal/database/backup.go` 用外部 `pg_dump -Fc`/`pg_restore` 做整库备份恢复：备份写 `<dump>.json` 清单（sha256、大小、每表行数、ledger 行数与最高版本），并用 `pg_restore --list` 校验归档包含全部 11 张业务表；恢复只接受“不存在的空库”，先校验校验和与大小，再建库、`pg_restore --exit-on-error`、重新连库逐表比对行数与 ledger，并验证 `events` 指纹 CHECK 与 `issues` 指纹唯一索引存在；校验和篡改、目标库已存在均被拒。本地 PostgreSQL 16.15 实测 `go test ./internal/database/ -run 'TestBackup|TestDelete|TestRetention'` 全绿：备份恢复后 11 张表行数、ledger、解法正文与 issue 标题一致；二次恢复同一库被拒；篡改 dump 报校验和失败；按项目删除前预览与实际删除的行数一致，`delete.scope` 审计记录操作者与计数、旧审计行仍在；时间边界严格“早于”且恰在边界的行保留；按 connector 删除后其他 connector 的事件与 issue 保留；三类保留各自生效（10 天前样本清空、100 天前事件删除、400 天前审计删除）且新样本与新审计保留；直接 `delete from audit_log` 被 55000 拒绝。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 30.1 s），全树隐私扫描 0 命中。偏差/边界：备份恢复依赖本机 `pg_dump`/`pg_restore`（可用 `ERRKB_PG_BIN` 指定目录，默认查 PATH 与 `%ProgramFiles%\PostgreSQL\16\bin`），属于已知 shortcut；DSN 重建只支持 `sslmode=disable|require` 两种；删除按 connector 时 issue 汇总不随单个 connector 删除（issue 跨 connector 聚合，证据删除后 issue 仍在）；OCR 帧不入库由采集层保证，本卡不涉及 |
| ☑ | EKB-027 | 建立性能和崩溃门槛 | 020-026 | 已完成：基准、环境、数据分布、查询分布与 `EXPLAIN ANALYZE` 全部记录在 `docs/benchmarks.md`；`internal/database/bench_test.go` 用确定性 SQL 生成 10 万 issue / 221803 事件（9 万单条、9900 小尾、100 条重尾，分类 45/35/15/5，11.1% 非 ASCII，五个项目各 2 万），同参数复现同分布（`TestPerformanceGeneratorIsDeterministic`），冒烟（4000 issue，有数据库即运行）绿，崩溃四场景真实 `Process.Kill` 全过。首次 10 万门槛未达标（`pg_trgm` p95 155.3 ms、完整检索 p95 226.0 ms），按卡标 ⚠ 阻塞并定位根因后修复：(1) `db/migrations/0007_occurrence_lookup.sql` 补 `occurrences_issue_latest_idx (issue_id, occurred_at desc, id desc)`——EKB-020 删 `(issue_id, event_id)` 唯一约束时带走了唯一覆盖“最新 occurrence”查询的索引，单次 lateral 206.8–236.9 ms 降到 0.025–0.28 ms；(2) `internal/database/candidates.go` 新增 `TrigramQueryMaxChars = 64`，把 `pg_trgm` 候选输入截到 64 字符（GIN 扫描随查询 trigram 数增长，100 字符 74–89 ms、64 字符 47–58 ms），Go 重排仍用全文，EKB-024 的零误合并与召回不变（distinct-lookalike 12/12、same-error 10/10，`same-error-009` 仍记为召回缺口），`TestFuzzyCandidatesCapTheQueryText` 锁定截断。复测（同生成器、同样本、同测法，每类 200 轮）：精确 p50 0.0 / p95 0.6 / p99 0.6 ms；`pg_trgm` p50 65.5 / p95 81.7 / p99 84.7 ms；完整检索 p50 68.7 / p95 84.5 / p99 87.9 ms，三类均低于 150 ms。崩溃门槛：未提交写、已提交写、批量写、迁移中途四种真实 kill 后已提交数据完整、无孤儿 occurrence、ledger 一致（迁移中断后 7 行，重跑只应用探针 0008 得 8 行，探针版本从 `LoadMigrations` 推导）、无残留 advisory lock。GiST 实测 444–593 ms（比 GIN 差 6–7 倍），维持 EKB-023 的 GIN 结论。`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 55.9 s，含冒烟与崩溃）；100k 全量基准仍由 `ERRKB_PERF_BENCH=1` 手动触发，P3 门槛达成 |

P3 门槛：PostgreSQL 成为唯一权威事实库；Markdown 只用于交换；并发、备份、恢复和检索基准全部通过。

### P4 本地服务 CLI 和高可靠采集

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-028 | 实现 `errkb daemon` | 019-027 | 已完成：`internal/service/daemon.go` 用 Windows 命名互斥体（默认 `Local\errkb-daemon`，kernel32 `CreateMutexW`/`CloseHandle`）保证单实例，不用 PID 文件；进程崩溃时内核释放互斥体，测试中真实子进程持锁后被 `Process.Kill`，随后能重新取锁并重启守护进程。生命周期：`StartDaemon` 取锁 → 打开日志 → 建立连接池 → 校验 `schema_migrations` 最高版本与 `LoadMigrations` 的期望值一致（不一致直接报错拒绝启动，不带着错版本运行）；`BeginRequest` 登记在途工作，`Shutdown` 先置关闭位并拒绝新请求，待在途计数归零后关闭自有连接池、关闭日志、释放互斥体（无 `os.Exit`，不留半写事务）。测试在在途事务提交前调用 `Shutdown`，确认它一直等待，提交后已提交记录恰为 1 行。健康状态 `Health` 可机读：版本、`ok`/`degraded`、数据库连通性、迁移当前/期望与是否一致、队列积压（EKB-033 前为可注入钩子，默认 0）、连接池 total/in_use/idle、在途请求数；`RefreshHealth` 经临时文件加改名原子写入 `health.json`。日志写前经 `internal/redact.Text(share=public)` 脱敏，按 `--log-max-bytes`（默认 4 MiB）轮转且只保留一个 `.1` 备份，只写文件、绝不写 stdout（stdout 留给 EKB-037 的 MCP stdio）。`cmd/errkb/daemon.go` 提供 `runDaemon`（`--dsn`/`--migrations`/`--lock-name`/`--health-file`/`--log-file`/`--log-max-bytes`/`--health-interval`，SIGINT/SIGTERM 经 `signal.NotifyContext`）；子命令分发表属 EKB-030，本卡只给入口函数。本地 Windows 实测 `go test ./internal/service/ -v` 全绿：第二实例被拒且错误含互斥体名、强杀后无残留锁并可重启、优雅停止等待在途事务、健康字段与 JSON 文件、未迁移 schema 拒绝启动且失败启动不泄漏互斥体、日志脱敏/轮转上限/不写 stdout；`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 53.7 s，service 5.5 s）。偏差：仅 Windows 构建（命名互斥体与 Named Pipe 宿主都是 Windows 目标）；`runDaemon` 尚未被 `main()` 调用（EKB-030 分发）；队列积压是钩子占位（EKB-033 实现） |
| ☑ | EKB-029 | 实现 Named Pipe 本地 API | 008、028 | 已完成：`internal/service/pipe.go` 用 Windows 命名管道（默认 `\\.\pipe\errkb`）承载本地 RPC，`internal/service/api.go` 只做信封校验、版本判定、错误码映射和路由，`pipe_test.go` 覆盖 17 项验收。默认**没有 TCP**：包内不导入 `net/http`、无任何 `Listen` 调用，运行时用 `GetExtendedTcpTable` 对比本进程 TCP 监听表（先用自建监听器验证读取器本身），启动管道前后完全一致。ACL 只授予当前用户和 SYSTEM：SDDL 由当前进程令牌的 SID 生成（`D:P(A;;GA;;;<sid>)(A;;GA;;;SY)`，不含 Everyone/Users/Authenticated Users），测试连上管道后用 `GetSecurityInfo` 读回真实 DACL 并把 ACE 的 SID 解析出来，断言集合恰好是 `{当前用户 SID, S-1-5-18}`（Windows 会把 SID 规范化成别名，例如 Administrator 显示 `LA`，所以比较 SID 而不是输入文本）。版本化：信封与每个操作的 payload 都要求 `schema_version="v1"`（`contract.DecodePipeRequest` / `requireVersion`），缺失或旧版本一律回 `invalid_request` 且消息可读（`want "v1", got ...`），未知操作同样拒绝并把原始 operation 回显给调用方。错误码用 `spec/local-api.cue` 的闭集：`invalid_request`/`not_found`/`unavailable`/`conflict`/`internal`；后端 `*APIError` 原样透传，`context.Canceled`/`DeadlineExceeded` 映射为可重试的 `unavailable`，其余为 `internal`。断线明确：服务不存在时 `winio.DialPipeContext` 快速返回错误（实测 <1 s，不挂起）；超大帧（默认 1 MiB 上限）返回 `invalid_request` 后关闭连接；`PipeOptions.BeginRequest` 钩子在守护进程关闭时以可重试 `unavailable` 拒绝新请求。并发正确：每连接单 goroutine 顺序应答、连接之间互不影响，6 连接 × 8 帧并发测试逐帧校验 `request_id` 与回显 note 一致，无串包。不吞文本、无业务逻辑：管道层不写日志、不打印捕获文本，`api.go` 不导入 `internal/database` 或 pgx，只把已校验请求交给 `Backend` 接口（真实后端在 EKB-030/037 接线）。测试还固定了「后端返回违约 payload 时由分发器拦截为 `internal`」这条自检。本地实测 `go test ./internal/service/ -v` 全绿（17 个管道用例 + 7 个守护进程用例，4.2 s），`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 43.4 s）。偏差：新增 `github.com/Microsoft/go-winio v0.6.2` 与 `golang.org/x/sys v0.32.0` 依赖（命名管道与 ACL 只能用 Win32 API，属三文件落点之外的 go.mod/go.sum 变更）；`Backend` 的真实实现留给 EKB-030/037，本卡不做业务；按 §6 本卡不实现 HTTP（因此“默认关闭”是没有任何 TCP 代码可开，未来若加必须默认 false、只绑 loopback 且要求随机 token） |
| ☑ | EKB-030 | 实现基础 CLI | 024、029 | 已完成：`cmd/errkb/main.go` 做子命令分派（`--json` 可出现在命令前后，分派前统一剥离；`daemon` 交给 EKB-028 的 `runDaemon`，`version`/`help` 内置，未知命令退出 2），`cmd/errkb/cli.go` 实现 `search`/`show`/`record`/`list`/`feedback`/`stats` 六个命令，`internal/service/client.go` 是唯一的本地传输：`winio.DialPipeContext` 连接默认管道 `\\.\pipe\errkb`，一请求一响应按行发送 `contract.PipeRequest`，用 `contract.DecodePipeResponse` 回读校验（信封版本、回显 `request_id`、payload 契约都复核），连接不存在时 1 秒内失败。CLI 不直连数据库：整个 `cmd/` 不导入 pgx，六个命令只走 `apiClient` 接口。`--json` 输出直接是 EKB-008 契约结构体（`SearchResponse`/`LookupResponse`/`RecordResponse`/`ListResponse`/`FeedbackResponse`/`StatsResponse`），端到端用契约 `Validate()` 复核，没有杜撰字段。人类输出解释推荐原因：标题、issue、指纹、置信度、`reason <match_reason>`，近似候选打印 `APPROXIMATE - confirm before reuse`，并打印 `detail`（契约 `Candidate` 没有独立环境字段，环境差异按 EKB-008 放在 `match_detail` 里，CLI 原样展示）。退出码语义化：0 命中/成功、1 内部错误、2 参数错误（含本地契约校验失败）、3 未找到（空候选、`not_found`）、4 守护进程未运行（`ErrServiceUnavailable` 或 `unavailable` 错误码），守护进程缺失时打印可读消息且无堆栈。CLI 无业务判断：不做归一化/脱敏/指纹/信任计算，`record` 只接受完整 `ErrorEvent` JSON（stdin 或 `--event`，经 `contract.DecodeErrorEvent` 校验；CLI 不生成事件，采集属 EKB-031 的 `run`），`feedback` 只提交 outcome 与 note，没有任何 trust 参数（`--trust` 直接是未定义标志，退出 2）。并发安全：`Client` 用互斥体串行化一连接上的请求/响应配对，8 worker × 5 次并发（真实管道 + 假后端）逐次核对回显 note 无串包；根命令 `--json` 在两处可用。本地实测 `go test ./cmd/... ./internal/service/ -count=1` 全绿（cmd 0.1 s、service 3.7 s），`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 45.3 s）；另用真实命名管道 + 假后端跑一次性端到端探针（22 项：六命令人类/JSON、契约校验、空结果、四种退出码、服务缺失、并发、未知命令、无 trust 参数）全过，探针已删除。偏差：按卡「期望输出」的测试要求新增 `cmd/errkb/cli_test.go` 与 `internal/service/client_test.go`（§11.1 只列三个生产文件），否则验收 10 `go test ./cmd/...` 无测试可跑；`cmd/errkb` 三个文件都带 `//go:build windows`（EKB-028 的 `runDaemon` 是 Windows-only，严格落点不允许新增平台 shim；Linux CI 的 `gofmt -l .` 仍检查格式，`go vet ./...`/`go test ./...` 静默跳过该包）。边界：守护进程尚未把真实 `Backend` 接到管道（EKB-028/029 只提供协议与入口），所以 `errkb search` 等命令在真实环境要等后端接线卡（EKB-034/035 或明确的后端卡）才有数据；本卡交付的是 CLI 与客户端传输，端到端已验证但用的是假后端 |
| ☑ | EKB-031 | 实现 `errkb run --` | 016、029 | 已完成：`internal/capture/run.go` 的 `Run(ctx, RunOptions)` 启动子进程并接管双流；退出码原样透传（0/1/127 实测一致，被取消/终止按 1 报失败，不改写）。stdout/stderr 用 `io.MultiWriter` 边写边透传、边捕获（不用“先存后打”），长时间命令与交互式程序不会阻塞；stdin 也透传。捕获有上限：默认每流 64 KiB，保留**前一半 + 后一半**，中间以 `[errkb: N bytes omitted from the middle of the output]` 标记；供 headline/指纹分析的文本不含标记（否则标记里的字节数会随输出长度漂移、破坏重跑稳定性）；200 KB 输出在 1 KiB 限额下实测截断、内存有界。环境摘要只取白名单：`os`、`arch`、shell 可执行文件名（`COMSPEC`/`SHELL` 取 basename）、`runtime_versions.go`，值再经 `redact.Text`；不是环境变量转储。只有失败才记录：退出码 0 不产生事件；非零退出先过 `core.Classify`（`source=command`、命令、退出码），瞬时错误（如 `i/o timeout`）返回 `Transient=true` 并**跳过永久记录路径**（重试计数与提升留 EKB-033 队列）。事件字段：`category` 固定 `command-exit`（契约的类别轴 = 采集来源，与 §1 领域分类是两套轴，领域分类仍在 `RunResult.Classification`，这是 §8.2/EKB-016 已知偏差的落地方式）、severity/transient 来自分类、headline 来自 `core.ExtractHeadline`、指纹 `core.Signature("command-exit", Normalize(脱敏后的 headline))`、`redacted_sample` 来自 `redact.Sample`、`channel` 为 B、uuid v4 新 `event_id`、confidence 0.5，写库前过 `Validate()`。重跑同错误实测命中同一 issue：两次执行 → 2 个事件、2 条 occurrence、1 个 issue、hits=2，指纹一致、event_id 不同（`RecordResult.Created` 表示“事件行新插入”，不是“issue 新建”）。信号/取消：`exec.CommandContext`，取消 `ctx` 后子进程被终止且 `Run` 在 1 秒内返回（无孤儿，`Wait` 回收）；控制台 Ctrl+C 由 Windows 直接送达子进程（包装器不新建进程组、不拦截）。测试 `run_test.go` 用“测试二进制自我重执行”的子进程助手覆盖 11 项：退出码透传、双流分离且成功不记录、启动失败报错、截断（含尾部保留）、stdin 透传、实时透传（助手打印后睡 400 ms，首字节在命令结束前到达）、取消、环境白名单与脱敏、真实数据库重跑幂等、瞬时分流、密钥脱敏（库内只有 `<secret>`）。本地实测 `go test ./internal/capture/` 全绿（2.2 s）、`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 40.8 s）。边界：CLI 的 `run` 子命令分派**本卡未接**——§11.1 严格落点只有两个文件，且 CLI 按设计不直连数据库（run 需要连接库或走守护进程），接线属于改计划/后续卡；瞬时的缓冲与提升属 EKB-033；调用方需先有 connector/session 行（身份供给不在本卡）。 |
| ☑ | EKB-032 | 实现日志尾随 | 016、029 | 已完成：`internal/capture/tail.go` 提供 `NewTailer`/`Poll`/`Run`/`Flush`/`Stats`，`tail_test.go` 用临时文件覆盖 12 项验收。编码：先看 BOM（UTF-8 `EF BB BF`、UTF-16LE `FF FE`、UTF-16BE `FE FF`），无 BOM 时按 ≤512 字节的 NUL 字节分布判定 UTF-16LE/BE（ASCII 文本在 UTF-16 里约一半是 0x00），其余按 UTF-8；解码跨轮询保留未完成的多字节/代理对（悬空高代理与奇数字节等到下一轮），非法字节替换为 U+FFFD 并计入 `Stats.InvalidBytes`，不 panic。偏移量按文件身份管理：每次轮询先 `os.Open` 再从打开句柄 `Stat`（Windows 只有句柄带文件 ID），用 `os.SameFile` 比较——重命名+新建同路径文件会被识别为轮转（`Rotations++` 并从头读），同一文件变短按截断处理（`Truncations++`）。行拼接：64 KiB 分块读，跨轮询的半行留在缓冲区，`\r` 去掉、空行跳过、超长行按 rune 截到 8 KiB 加 `…`，无换行的部分缓冲有上限。重复抑制有界：只有**连续相同**的行合并为一个 burst，`Count` 精确（测试 5 行 + 1 行 → `{boom-1 ×5}` 与 `{boom-2 ×1}`，`LinesRead=6`、`Emissions=2`）；burst 在出现不同行、等待超过 `DuplicateHold`（默认 1 s）或 `Flush()` 时发出，不会“同一文本永不记录”。分类与入库：每行以 `source=log` 过 `core.Classify`（不能用 `source=command`——那会把每行都归到 build），`unknown` 与瞬时的行只计入 `Skipped` 不落永久路径；事件 `channel=C`、`category=command-exit`（与包装执行同指纹轴）、severity 取自分类、headline 经 `redact.Text`、指纹 `core.Signature("command-exit", Normalize(脱敏 headline))`、`redacted_sample`、`evidence="log tail <basename>"`（重复时附 `(repeated N times)`）、confidence 0.5，入库前 `Validate()`。缺失文件不是错误：`Poll` 返回 0，`Run` 在无新字节时把间隔加倍到 `MaxBackoff`（默认 200 ms→5 s）并在有数据时复位，250 ms 内实测仅约 7 次轮询（非忙等）。测试覆盖：UTF-8 带/不带 BOM（含中文）、UTF-16LE/BE BOM、无 BOM 推断、轮转（`os.Rename`+新建）、截断、半行、重复 burst 精确计数、非法字节可观测、缺失文件退避、只记录分类失败、脱敏先于记录（`<secret>`，`Validate()` 拦截）、真实数据库落库 1 event/1 issue。本地实测 `go test ./internal/capture/` 全绿（3.3 s），`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 44.9 s）；privacy-guard 正则 0 命中。边界：尚未接入 CLI（CLI 只走管道，尾随需要数据库路径，接线需改计划）；重复窗口默认 1 s、跨 burst 不去重；瞬时行不缓冲（重试提升属 EKB-033）；文件身份用打开句柄判等（Unix 上 `os.SameFile` 同样成立，但本卡只在 Windows 实测） |
| ☑ | EKB-033 | 实现断线脱敏队列 | 014、028 | 已完成（Go 队列部分）：`internal/service/queue.go` 提供 `OpenQueue`/`Enqueue`/`Replay`/`Run`/`Stats`，`queue_test.go` 覆盖 12 项验收。入队前脱敏：`Enqueue` 先对 headline、normalized_message、redacted_sample、evidence、command、error_code、tool_name 走 `redact.Text`/`redact.Sample`，再 `Validate()`——仍像敏感内容的事件被拒绝而不是落盘，所以队列文件里只会是脱敏内容。磁盘持久化与原子写：每个事件一个 `<入队纳秒>-<event_id>.json`，先写 `.tmp` 再改名；spool 目录 0700、文件 0600；同一 `event_id` 已在队列中时 `Enqueue` 返回 `false`，重试不会让队列膨胀。双上限：`MaxEvents`/`MaxBytes`/`MaxAge`（默认 1000 条 / 32 MiB / 72 小时），超限策略明确为“丢弃最旧”，丢弃与过期分别计入 `Dropped`/`Expired`；最新一条永不丢弃（单个事件大于字节上限时暂时超预算，代码注释说明"刚发生的失败比预算更重要"）。幂等重放：`Replay` 按文件名（入队时间）从旧到新调用 recorder（即 `database.RecordEvent` → `errkb_record_event`），判重依赖 `event_id` 唯一性而不是队列侧的“已发送”标记；成功删文件，失败停止本轮并保留剩余文件（`ReplayErrors`/`LastError` 可观测）；无法解码的文件改名 `.corrupt` 而不是删除。恢复探测：`Run(ctx, QueueRunOptions{Available, Recorder, PollInterval, MaxBackoff})` 探测可用后重放，失败指数退避（默认 2 s→30 s）。非阻塞：`Enqueue` 只做一次小文件写入，不访问数据库或网络；目录消失时立即报错返回。测试覆盖：脱敏先于落盘（原始 token 不出现在文件里、`<secret>` 出现），未通过校验的事件被拒，重复 `event_id` 不重复入队，计数上限丢最旧并计数，字节上限丢最旧（最新一条保留），过期清理，目录消失快速失败，真实数据库重放 2 事件/2 issue，崩溃后重放仍只有 1 个 event（`event_id` 唯一），数据库不可用时保留文件并记录错误，`Run` 在探测恢复后重放并清空。本地实测 `go test ./internal/service/ -count=1` 全绿（4.6 s，含守护进程与管道测试）、`gofmt -l` 空、`go vet ./...`、`go test ./...` 全绿（database 46.6 s、service 6.8 s），privacy-guard 正则 0 命中。偏差（经项目所有者 2026-10-19 批准）：`src/state.ts` 的缩减未在本卡实施——移除权威命中/可信度会连带改 `src/plugin.ts`（`stateTrustStore`/`addHit`/`effectiveEntry`）与 `tests/state.test.ts`、`tests/injection.test.ts:1003`、`tests/plugin.test.ts:472`，超出本卡“三文件”落点，且 §8.3 已把 `tests/state.test.ts` 的重写排在 EKB-038；已在 EKB-038 行与 §11.1 行标注该顺延项。边界：队列尚未接进守护进程的记录路径（daemon 的 `Backend` 尚未接线，属 EKB-034/035 或显式后端卡）；瞬时失败的重试计数/提升仍只被跳过，未进队列统计；队列只缓冲已脱敏事件，不做采集 |
| ☑ | EKB-034 | 实现 `errkb doctor` | 028-033 | 已完成：`internal/service/doctor.go` 提供 `RunDoctor(ctx, DoctorOptions) DoctorReport`，八类检查（版本、数据库、迁移、扩展、Pipe、连接器、队列、隐私）各自独立执行、互不短路——数据库不可达时其余七项照常输出，报告永远完整，函数不返回 error。三态 `ok`/`degraded`/`failing`：pg_trgm 缺失记 degraded（精确检索仍可用）、Pipe ACL 不是“当前用户 + SYSTEM”记 failing、迁移 ledger 与文件校验和不一致记 failing、ledger 落后记 degraded。复用 EKB-028 的 `Health` 结构：报告内嵌 `Health`（database/migrations/queue 与守护进程同源同义），`HealthVersion`/`healthOK`/`healthDegraded` 直接复用，托盘（P6）与 CLI 读同一份判断。只读：不建表、不修配置、不重启、不跑 `Migrate`；迁移一致性靠直接查询 `schema_migrations(version, checksum)` 与 `database.LoadMigrations` 的 sha256 对比；队列只 `os.ReadDir`，目录不存在时视为空而不是创建它。自脱敏：DSN 从不打印，错误文本经 `doctorText` 去掉 DSN、`password=`、URL 凭据与配置/队列/迁移路径（替换为 `<dsn>`/`<password>`/`<path>`），Pipe 详情只含 SID 与描述符。每个非 ok 项都有一行 `fix`。数据库检查打印 PostgreSQL 版本、库名与 `pg_stat_activity` 连接数；迁移打印 applied/expected；扩展打印 `pg_trgm` 版本；Pipe 用短超时拨号判断是否监听，并用与服务端同一个 `pipeSecurityDescriptor()` 解析 ACE SID、断言只授予当前用户与 SYSTEM，同时报告 HTTP 是否关闭；连接器打印名称与通道等级；队列打印 pending/bytes/最旧年龄/是否超限（默认 1000 条 / 32 MiB）；隐私打印规则摘要 `redact.SourceDigest`、share、采集开关、通知时机、保留期与额外模式数。`cmd/errkb/doctor.go` 提供 `--json`（直接输出 `DoctorReport`，字段与文本同源）与人类输出，退出码 0（ok/degraded）/1（failing），参数 `--dsn/--migrations/--config/--queue-dir/--pipe/--timeout`。本地实测 `go test ./internal/service/` 全绿（7.8 s）、`go test ./cmd/...` 全绿、`go test ./... -count=1` 全绿（database 45 s），`gofmt -l` 空、`go vet ./...` 干净、privacy-guard 0 命中。测试 8 项：八类全绿且顺序固定；数据库断开时其余七项照常（database/migrations/extension/connectors=failing、pipe=degraded、version/queue/privacy=ok，非 ok 项都有 fix）；pg_trgm 缺失为 degraded 而非 failing；连接串密码不出现在 JSON/文本；只读证明（doctor 前后 10 张表与 ledger 行数完全一致，且不创建队列目录）；队列超限报告 pending/over_limit 与 health backlog；CLI 的 `--json` 与人类输出 fix 提示。偏差：§11.1 只列两个生产文件，但按卡“期望输出”需要可运行测试与命令入口，故新增 `internal/service/doctor_test.go`、`cmd/errkb/doctor_test.go`，并在 `cmd/errkb/main.go` 增加 `doctor` 分派与 usage 行（否则命令无法到达）；`internal/service/doctor.go` 与 `cmd/errkb/doctor.go` 带 `//go:build windows`（复用 EKB-028 的 Windows-only `Health` 并需要管道 ACL，严格落点不允许平台 shim；Linux CI 的 go job 仍检查格式、静默跳过测试）。边界：不接真实 `Backend`（本卡只做诊断，数据命令仍等后端接线卡）；连接器通道等级取自 `connectors` 表而非 EKB-007 能力契约文件（能力契约尚无运行时存放位置，待采集卡落位）；HTTP 开关按配置文件判断，未配置时按默认关闭 |
| ☑ | EKB-035 | 完成 CLI 技术错误闭环 | 030-034 | 已完成（2026-10-19）：`tests/e2e/cli_loop_test.go` 是真实场景脚本——构建真实 `./cmd/errkb`、用 `--pipe`/`--lock-name` 启动真实 `errkb daemon`、跑真实失败命令 `cmd /c type <不存在的路径>` 两次，并断言报文与 SQL 行；`docs/e2e-cli-loop.md` 抄录本次运行产物（命令、首次/二次输出、检索与 outcome 行、无模型/无网络证据）。实测：首次运行退出码透传 1、`created=true`、`lookup.candidates[]` 无任何 `solution` 字段、`issues=1`、`retrievals` 1 行且 `match_fields={fingerprint}`；经 `CreateSolution → AddSolutionVersion → TransitionSolution(verified)` 录入解法（来自数据库，非硬编码）后第二次运行返回同一 issue、解法正文、`source e2e`、`applies to os=windows`、`errkb_trust` 0.50；`feedback --outcome success` 写出 outcome 后信任度查询期变 0.60；近重复消息仅以 `jaccard_rerank` 近似候选返回（无 `exact_fingerprint`），人读输出带 `APPROXIMATE - confirm before reuse`；`stats` 总计 events 3 / issues 2 / solutions 1 / outcomes 1。真脚本（可重复执行）与 8 项断言全绿；全程无模型调用、无外联（`go.mod` 无模型 SDK、一方代码无 `net/http`，只有本地管道与 `127.0.0.1:5432`）。闭环跑通前按要求回退到对应卡补齐五处真实缺口（不在本卡打补丁）：EKB-008 契约新增可选 `Candidate.solution`（`spec/mcp-tools.cue`/`internal/contract/tools.go`，加性字段→兼容）、EKB-024 `Retrieve` 附着最新已验证解法版本与 `errkb_trust`、新增 `internal/database/overview.go` 的 list/stats、EKB-029/028 守护进程真正监听并在 `Shutdown` 关闭 Named Pipe、EKB-030 CLI `run` 子命令与启动参数（`cmd/errkb/run.go`）；另新增 `internal/service/backend.go`（真实 `Backend` 接 PostgreSQL）与其测试、`tests/e2e` 测试文件。验证：`go test ./... -count=1` 全绿（database 56.4 s、service 10.8 s、e2e 4.1 s），`pnpm test` 732 项全绿，`gofmt -l` 空、`go vet ./...` 干净，privacy-guard 0 命中。偏差：本卡触碰 EKB-008/024/028/029/030 的文件与 `cmd/errkb/main.go` 分派，超出卡内「仅 `tests/e2e/` 与计划行」的严格落点，按卡内「闭环跑不通回退对应卡修」执行。边界：仍无 `db migrate` 命令（e2e 用 Go 入口建 schema），`run` 需要显式 connector/session，解法录入无 CLI 动词（P4 演示允许走 Go API，但必须来自数据库）；P4 收口：EKB-028…034 全部 ☑，P4 门槛达成 |

P4 是第一个可用 MVP 门槛：不依赖任何特定大模型，用户已经能采集、搜索、记录解法并复用。

### P5 宿主连接器和诊断前回传

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-036 | 定义连接器 SDK 和能力探针 | 006-008、029 | 已完成（2026-10-19）：`spec/connector.cue` 定义四条 SDK 调用契约（capture/lookup/notice/feedback，统一 v1 信封；lookup 最多 3 个带原因的候选且显式 `degraded`；notice 结果含 `chars`/`tokens`；feedback 只记录结果），九个结构体、61 个字段、34 个必填登记进 `spec/compat.cue` 的新 `connector` 清单（`unknown_fields: tolerate`——SDK 载荷由 encoding/json 解码并接受新版本适配器的额外字段，严格的敌意输入拒绝留在本地 API 解码器），本地运行分类器得到 `[R10] connector: contract added (compatible)` 与 `compat: OK`。`internal/capture/connector.go` 提供 `Connector` 四接口与 `CapabilityProbe` 接口、`DegradedError`/`Degradation` 显式降级（缺失字段与回退通道可枚举、可直接返回）、`ProbeReport`/`ProbeConnector`（主机标识、版本、探测时间、缺失列表）和医生可消费的 `DoctorCheck`（degraded 状态、`falls back to` 摘要、非空修复建议）；探测是针对真实宿主的动作而非读配置，测试用计数器证明每次报告都来自一次真实探测；`connector.cue` 不重复 `connector-capability.cue` 的宿主/通道/字段/回退/探针字段，源码级测试防止适配器重实现分类、脱敏、存储、检索和可信度。实测 `go test ./internal/capture/` 通过（全部 21 个用例，其中 8 个新增），`gofmt`/`go vet` 干净，`cue fmt`/`cue vet` 通过，隐私守卫 0 命中；边界：具体适配器、真实探针实现和 doctor 接线属于 EKB-038/040/041/043，窗口标题永远不作为能力依据 |
| ☑ | EKB-037 | 实现 MCP stdio 服务 | 008、030 | 已完成：`internal/mcp/server.go` 实现 JSON-RPC 2.0 over stdio（`initialize`/`ping`/`tools/list`/`tools/call`，错误码 -32700/-32601/-32602/-32603，帧上限 4 MiB、并发上限 8，响应按请求顺序串行写出，日志只进 stderr）；`internal/mcp/tools.go` 把 `err_lookup`、`err_search`、`err_record`、`err_feedback`、`err_list`、`err_stats` 六个工具包在 `Backend` 接口上，参数严格解码（未知字段拒绝）、`schema_version` 缺省回填 v1，请求与响应都过 `contract` 的 `Validate()`；工具描述与 `initialize.instructions` 明写 MCP 不是被动捕获（`spec/mcp-tools.cue` 原文 + 更正段落）；`cmd/errkb/mcp.go` 用 `pipeBackend` 每次调用拨一次本地管道（`ERRKB_PIPE` 可改，守护进程重启不会杀掉会话），`cmd/errkb/main.go` 增加 `mcp` 分派与用法行（偏差，见 §11.1）。测试：`internal/mcp/tools_test.go` 八个测试全绿——initialize 契约（协议 2024-11-05、serverInfo、更正说明）、恰好六个工具且无 capture/subscribe/watch/push 被动面、六工具返回可 `Validate()` 的契约 JSON、stdout 只有 JSON-RPC 帧（坏帧 → -32700、未知方法 → -32601）且日志只进 stderr、未知工具/未知字段/空查询 → -32602 且后端未被调用、随后 ping 仍应答、五路并发（首个慢 200 ms）按请求顺序返回、通知无应答、EOF 干净退出。真实二进制冒烟：`errkb mcp` 收到 4 个请求返回 4 行 stdout 全部 JSON-RPC（initialize、6 个工具、守护进程未启动时 `tools/call` → -32603 且进程继续、ping 应答），stderr 有启动日志。`go test ./internal/mcp/` 绿、`go build ./...`/`go vet ./...` 干净、privacy-guard 0 命中；`internal/mcp` 跨平台（Linux CI 的 `go` job 也跑测试），`cmd/errkb` 仍仅 Windows。 |
| ☑ | EKB-038 | 将 DSH 插件重构为连接器（2026-10-19 拆分为 EKB-038a/b/c/d） | 003、036 | 已完成（2026-10-19）：四个子卡全部落地——038a 核心 capture 操作、038b 核心写回 `record_solution`/`set_status`、038c 连接器库（Pipe 客户端 + 观测队列 + 薄适配层）、038d 切换薄适配器并重写测试。原验收保持：`src/plugin.ts` 和 `src/tools.ts` 全部经本地 Pipe 调核心 API（无本地知识库流水线）；断线行为显式为“原始观测入 `src/state.ts` 队列、恢复后按序重放”，核心不可达时工具返回明确降级；权威命中只在数据库，适配器按 `observation_id` 幂等，一次失败只增一次；适配器路径无分类/脱敏/指纹/相似度实现（grep 证据与测试断言）。EKB-033 顺延项已在 038d 完成：`src/state.ts` 只保留连接器本地队列（v2 `state.json`），`stateTrustStore`/`addHit`/`effectiveEntry` 删除，FixTrust 改为内存态、outcome 经 `feedback` 上报，`tests/state.test.ts` 重写，`pnpm test` 17 文件 720 例全绿。宿主契约未变：五个工具名、参数键与输出 `{schema, render}` 形状逐项一致。边界：DSH profile 的真实“错误→查询→解法→outcome”验收在 EKB-039（P5 门槛） |
| ☑ | EKB-038a | 核心 capture 操作与观测流水线（从 EKB-038 拆分） | 036、037 | 已完成（2026-10-19）：Pipe 新增 `capture` 操作，复用 EKB-036 `#Observation` 契约（`#Observation.message` 放宽为原始文本——适配器不得脱敏，核心在写库前脱敏；新增可选 `channel`（默认 A）与 `observation_id`（幂等键））；`internal/capture/observation.go` 走与 `errkb run` 相同的 headline/分类/脱敏/归一化/指纹流水线并在写库前 `Validate()`；`internal/database/capture.go` 把连接器名与会话键解析成 uuid，`StoredEventByEventID` 在记录前吸收重传（EKB-020 把每次调用计为一次出现，重放不能进入两次）；`SQLBackend.Capture` 与 `API` 路由接线。测试 4 例：重放幂等（events/occurrences/issues 各 1、身份一致、category=tool、channel=A、headline 带 EPERM）、原始秘密先脱敏（`<secret>` 出现、原文不落库）、非法信封拒绝且不写事件、适配器身份只建一次（两会话 1 connector / 2 sessions）。本地实测：compat 检查 `[R1]×2 + [R5] operation=capture (compatible)`、`compat: OK`；`gofmt -l` 空、`go vet`、`cue vet`、`go test ./...` 全绿（capture 12.2 s、database 58.5 s、service 10.3 s、e2e 5.3 s）。偏差：为让适配器零本地能力回填了 EKB-008/029/036 契约（所有者 2026-10-19 批准）；边界：TS 客户端与插件接线在 038b，测试收口在 038c |
| ☑ | EKB-038b | 核心写回操作 record_solution 与 set_status（从 EKB-038 回填，2026-10-19） | 038a、036-037 | 已完成（2026-10-19）：本地 API 与 MCP 扩为九操作，`record_solution` 在单个事务里复用候选/已验证解法或新建、追加版本（`version = max+1`）、把候选转为 verified、把 issue 置为 `resolved`，`set_status` 用封闭集合 `open/resolved/wontfix/misjudged` 更新 issue；`internal/database/solution.go` 抽出 `solutionWriter`（`*pgx.Conn` 与 `pgx.Tx` 通用），新增 `internal/database/solution_record.go`；`internal/service/backend.go` 新增两方法与 `environmentFromContract`，`pgx.ErrNoRows` 映射为 `not_found`；`internal/mcp/tools.go` 扩到八个工具（`err_record_solution`、`err_set_status`），`spec/mcp-tools.cue`/`spec/local-api.cue` 同步（`#IssueStatus`、`#SolutionEnvironment` 与四个请求/响应定义）；`cmd/errkb` 的 apiClient、`internal/service/client.go` 的 `Client`、`cmd/errkb/mcp.go` 的 `pipeBackend` 全部补齐。测试：契约拒绝非法信封；数据库 4 例（验证并解决、追加第二版、缺 issue → `ErrNoRows`、改状态/非法状态/缺 issue）；MCP 八工具列表、全工具契约 JSON、两个新工具请求落到后端、非法参数 -32602；pipe 路由九操作。实测：compat 自检 18 条规则，仅兼容变更（`[R1]` 可选字段、`[R5]` 枚举值、`[R17]` 新定义、`[R18]` 新枚举集合）+ `compat: OK`；`gofmt -l` 空、`go vet`、`cue vet`、`go test ./...` 全绿（database 51.1 s、service 10.4 s、mcp 0.3 s、e2e 6.8 s）。偏差：回填 EKB-008/029/036 契约（所有者批准）；TS 适配器、state 队列与测试重写整体移入 038c（拆开会造成 CI 红或留下未测适配器，已向所有者说明） |
| ☑ | EKB-038c | 连接器库：Pipe 客户端、观测队列与薄适配层（从 EKB-038 拆分，2026-10-19） | 038b | 已完成（2026-10-19）：新增 `src/connector.ts`——每次调用拨一次本地管道的 NDJSON 客户端（`schema_version`/`request_id`/`operation`/`payload` 信封，校验版本、请求号与操作；超时、不可达、提前关闭、非法响应映射为封闭 `CoreError`，只有 `unavailable`/`retryable` 才入队）；`createConnector` 薄适配层（`observation_id` 幂等、断线入队、按序重放且首个失败即停、`pending()` 计数）；观测构造器 `observationFromAgentError`/`observationFromToolResult`（原始文本、命令、退出码、工具名、证据；成功结果不记录）；`notice()` 由核心候选生成 hit/near/no-fix 文案（近似项带“先验证”措辞）。`src/state.ts` 追加 `createObservationQueue`：版本 2 的 `state.json`（`{version:2,dropped,queue:[…]}`），原子写 + 知识库锁、按 `observation_id` 去重、按观测数与字节数封顶并丢弃最旧、v1 文件迁移为空队列（命中/可信度不再落盘）。`src/index.ts` 追加 re-export 让模块进入构建产物（未接线）。测试 `tests/connector.test.ts` 27 例：信封与四种传输失败、超时、错误映射、九个操作路由、捕获/入队/非重试错误、按序重放与中途失败、通知渲染、两种观测构造、队列顺序/去重/上限/迁移/坏文件/空 id。实测：`pnpm typecheck` 通过；`pnpm test` 16 文件 759 例全绿（原 732）；`connector.ts` 覆盖率 98.8% 行、`state.ts` 99.6%；`pnpm build` 通过（产物含 connector 类型面）；prettier 干净、oxlint 0 警告。偏差：新增 `src/connector.ts` 与 `tests/connector.test.ts`（在本卡“适配器与测试同卡”的决定下先落库层，所有者批准），本卡不切换默认路径 |
| ☑ | EKB-038d | 切换薄适配器并重写测试（从 EKB-038 拆分，2026-10-19） | 038c | 已完成（2026-10-19）：`src/plugin.ts` 重写为薄适配器（Recorder 改由 `Connector` 提供：capture→`capture` 观测、lookup→`lookup`/`lookupIssue`/`lookupFingerprint`/`search`、entries→`list`、recordFix→`record_solution`、setStatus→`set_status`；FixTrust 改为内存态，注入层保留但默认信任改为 `memoryTrustStore`，session-start 顺带 `replay()`）；`src/tools.ts` 五工具全部代理核心（`err_lookup` 的 12 位十六进制走指纹、文本走 lookup，未命中用 `search` 给最近候选；`err_record` 走 lookup→capture→`record_solution`/`set_status`；`err_forget` 改 wontfix；`err_stats` 用 `list`+注入计数）；`src/index.ts` 切换默认路径并新增 `pipe`/`pipeTimeoutMs`/`offlineMaxEvents`/`offlineMaxBytes`/`connectorId`/`projectKey` 设置，装配观测队列 + `createConnector`，启动日志改为“知识库就是本地核心”；`src/state.ts` 删除 v1 权威命中/可信度，只保留队列。让候选与列表携带渲染所需信息：核心回填可选 `Candidate.hits`、`ListItem.hits/status/fix`（`spec/mcp-tools.cue`、`internal/contract`、`rerank.go`、`overview.go`、`backend.go`、`spec/compat.cue`）。测试重写：新增 `tests/support.ts`（假核心/假连接器/内存队列），`tests/plugin.test.ts` 39 例、`tests/tools.test.ts` 37 例、`tests/injection.test.ts` 30 例、`tests/state.test.ts` 13 例、`tests/settings.test.ts` 53 例、`tests/connector.test.ts` 30 例。实测：`pnpm typecheck` 通过；`pnpm test` 17 文件 720 例全绿，语句/行覆盖率 99.73%（阈值 99%）；`pnpm build` 通过；`pnpm lint` 0 警告；prettier 干净；`gofmt -l` 空、`go vet`、`cue vet`、`go test ./...` 全绿（database 57.2 s、capture 12.2 s、service 10.0 s、mcp 6.7 s、e2e 4.9 s）；compat 检查 18 条规则，仅 `[R1]` 四个可选字段（`Candidate.hits`、`ListItem.fix/hits/status`）且 `compat: OK`，未升版本。偏差：回填 EKB-008 契约可选字段（所有者已批准的同类回填）；`tests/settings.test.ts` 增补接线测试。边界：真实 profile 的端到端验收在 EKB-039 |
| ☑ | EKB-039 | 完成 DSH 未实现项 | 038 | 已完成（2026-10-20）：`src/plugin.ts` 接通 `agent/request-error` 瀑布监听（`await next()`、原样返回下游结果、绝不接管重试，失败观测进入捕获线程）；`providers` 白名单生效（默认 `["*"]`，无 provider 的载荷保留；被拒 provider 同时把消息标记为已见，配对的 `agent/error` 被去重）；双触发去重（会话 + 首行消息键，1.5 秒窗口，单元测试证明同一次失败的两个来源只计一次）；`resolve-detect.ts` 只上报结果——`resolved()` 通过 `feedback` 写 outcome，本地 `FixTrust` 从 `Injection`/`InjectionDeps`/`err_stats` 移除，`errkb_trust()` 为唯一可信度来源。`src/index.ts` 把 providers 接入 recorder 并注册瀑布监听。真实 DSH headless profile 验收（一次性 profile `errkb-e2e`，connectorId `dsh-e2e`，pipe `\\.\pipe\errkb-e2e2`，channel A）：非法模型请求每次失败恰好一条事件（重复运行只增 hits），数据库中有 `[exit code: 1]` issue（`32d2f0fb-e30f-4c0b-9a46-abc1067e97e1`，hits 10）与 llm INVALID_REQUEST issue 及其 verified 解法；模型调用 `err_lookup 'exit code: 1'` 从核心取回 issue id 与 fix 原文（标记 fuzzy、非精确不复用），`err_stats` 返回 totals；全程无本地分类/脱敏/指纹/匹配调用，检索不依赖模型；会话记录经 zstd 帧解码可见常驻 errkb system-prompt 提示。实测中还抓出并修复三处真实契约缺陷：`#NoSensitive` 的 request id 规则误拒脱敏标签 `request_id: <request-id>`（CUE 与 Go 改为要求 ID 形态的值，补正反测试）；适配器 lookup 载荷误带 `connector_id`/`session_id` 且用 `issue_id`（改为 `issue`）、list 请求 `limit: 200` 超出契约 1..100（改 100）；lookup/search 放宽为接受原始文本（CUE 去除 `#NoSensitive`，Go 在检索前规范化，路径/行号/request id 可命中）。实测：`pnpm typecheck`、`pnpm test` 16 文件 733 例全绿（语句/行覆盖率 99.4%）、`pnpm build`、`pnpm lint` 0 警告、prettier 干净；`gofmt -l` 空、`go vet`、`cue vet`、`go test ./...` 全绿（database 58.7 s、service 8.0 s、e2e 3.4 s）；兼容检查 18 条规则、仅 `compat: OK`（本次只放宽文本约束，字段/枚举/版本未变）。偏差：为跑通真实 profile 回填 `spec/mcp-tools.cue`、`spec/error-event.cue`、`internal/contract`、`internal/service/backend.go` 及对应 Go 测试（EKB-008 同类回填），改动大于卡片三文件落点，均已登记。边界：provider 过滤只作用于 `agent/request-error`（`agent/error` 无 provider 字段，靠去重键抑制）；transient 重试策略仍归宿主；真实 profile 为一次性验收，产物在仓库外 |
| ☑ | EKB-040 | 实现 Codex 连接器 | 036-037 | 已完成（2026-10-20）：`connectors/codex/probe.md` 记录 Codex CLI 0.156.1 的能力探针（`codex mcp add/list/get` 可用；`Stop` 钩子实测载荷含 session_id/turn_id/transcript_path/last_assistant_message；十个生命周期事件里没有 LLM 请求错误事件；宿主账号配额耗尽、本地 Ollama 模型无工具调用能力，`PostToolUse` 未能实测，如实记为未测）。连接器落在 `connectors/codex/`：README、capability.json（channel B，captures 21 字段，fallback C 记录 transcript 方案）、hooks.toml（实测 Stop 样例）、errkb-codex.ps1 包装脚本。采集走 channel B：`errkb run --connector codex-cli --session … -- codex exec …`；查询/记录走 MCP 八个工具。真实验收：真实配额失败首次建 issue `6a14be33…`（fingerprint `d0e12d3976ad`），经 MCP `err_record_solution` 记录解法、`err_lookup` 按精确指纹返回解法（trust 0.5）、`err_feedback` 记 outcome 后 trust 0.6，第二次运行打印同一 issue 的已知解法（来源 `codex-adapter`、适用 `os=windows, connector=codex-cli`、版本 `df9a5f0f…`）；DB 中 issues/hits=2/status=resolved、retrievals=3、outcomes=1 对应为证。修复回填：`cmd/errkb/run.go` 改走 EKB-038a 的 `capture` 操作（connector/session 用名字，避免 CLI 伪造 UUID），`internal/service/client.go` 增加 `Capture`，`cmd/errkb/cli.go` 接口与 `cli_test.go` 假客户端同步。边界：未实现 UI 抓取；进程级捕获看不到 Codex 内部重试成功；Hook 捕获仍需有配额的机器复测 |
| ☑ | EKB-041 | 实现 Claude Code 连接器 | 036-037 | 已完成（2026-10-20）：先记录能力探针 `connectors/claude/probe.md`（Claude Code 2.1.282：Hook 命令走 POSIX 风格 shell，路径必须用正斜杠，否则 `C:\…` 丢分隔符后以退出码 127 失败；`PostToolUseFailure`/`Stop`/`SessionStart` 载荷实测；十类生命周期事件里没有模型请求错误事件；`--output-format json|stream-json` 结构实测；MCP `claude mcp add/list` 注册、健康检查与 `mcp__errkb__err_*` 命名实测）。连接器文件：`README.md`、`capability.json`（通道 A，captures 21 字段，降级 C）、`hooks.json`（PostToolUseFailure→errkb-claude-hook.ps1，SessionStart→errkb-session-hint.ps1）、`mcp.json`（errkb.exe mcp）、`errkb-claude-hook.ps1`（确定性 observation_id = md5(会话|工具|输入|时间)，项目键取自 cwd，Named Pipe NDJSON，失败只写 `%LOCALAPPDATA%\errkb\claude-hook.log` 并退出 0，绝不打断宿主）、`errkb-session-hint.ps1`、`errkb-claude.ps1`（`claude -p` 包装）。真实闭环（真实 Claude Code 2.1.282 + 一次性数据库）：Hook 捕获真实 `ls` 失败→issue `28c2ad66-c8cc-4122-a31b-98e472302e75`（fingerprint `96d3ee6f967a`、tool_name Bash、exit 2、channel A）；MCP `err_lookup` 命中（jaccard_rerank 0.55，近似标记）→`err_record_solution` 版本 `ef7ba96b-8cd5-4e09-b921-ad47c3e397f4`→`err_feedback success`（outcome `ef756a0b…`）→trust 0.50→0.60；重放同一载荷 `created=false`，不重复计数；包装器捕获非瞬时的未知参数失败→issue `1e7c32f3-3ec3-43e6-9a73-f96229033abf`（channel B）→录入解法 `77f76db9…` 后下一次运行打印 `fix … source claude trust 0.60 version …`。数据库总计：issues 2、events 4（A+B）、occurrences 4、solutions 2、solution_versions 2、outcomes 2、retrievals 5，两条 issue 均 `resolved`、两条解法 trust 0.60。偏差：宿主模型代理不可用，Hook 载荷由本地 mock endpoint 驱动（工具调用与失败真实，仅模型回答合成，探针已注明复测条件）；回填 `cmd/errkb/run.go` 在 capture 请求中显式 `Channel: "B"`（EKB-040 回填遗漏，此前包装器事件被误记为 channel A）。边界：模型 API 失败没有 Hook 事件，走包装器；transient API 失败按 EKB-016 策略不计入；真实模型会话仍需在可用代理的机器上复测 |
| ☑ | EKB-042 | 建立其他宿主配置模板 | 031-037 | 已完成（2026-10-20）：`connectors/templates/cli-log/config.yaml` 提供 B/C/E 三种通用模板（CLI 包装、日志、手动），`README.md` 明确「不是专用适配器」的边界与当前可用性——B 直接可跑（`errkb run -- <host-cli>`），C 记录日志路径/编码/轮转意图但日志尾随尚无 CLI 入口（不虚假声明已接线），E 用手工管道（`… | errkb run -- …`）录入。为使模板可被配置校验，回填 `spec/config.cue` 可选 `templates` 块（cli/log/manual，命令非空、编码白名单 utf-8|utf-16、轮转最大字节与保留数为正）与 `internal/contract/config.go` 校验，新增模板接受/拒绝/装载随附模板三组测试；兼容门为「新增可选父节点下的必填字段」新增 R19 规则，`spec/compat.cue` 与 CI 内联检查器同步且自测 19 条规则全过。实测：`errkb doctor --config connectors/templates/cli-log/config.yaml` 返回 ok（迁移 0007、默认管道可连、connectors 详情 `codex-cli, dsh-e2e, generic-cli` 通道 `A, A, B`；`generic-cli` 由一次真实包装失败建立，issue `f1b0ec03…`）；`cue vet`、`go test ./internal/contract/`、`prettier --check connectors` 均绿，兼容检查 `compat: OK`。偏差：模板字段需要配置契约扩展（超出两文件落点），按 038/039 的回填方式记录。边界：不提供 DeepSeek/Kimi/Qianwen 专用解析或声明；C 模板在日志尾随接入 CLI 前只是配置意图 |
| ☑ | EKB-043 | 建立连接器兼容矩阵 | 038-042 | 已完成（2026-10-20）：`docs/connector-matrix.md` 与 `docs/connector-matrix.zh-CN.md` 同构落地，逐宿主、逐版本、逐通道记录实测能力，每格可追溯到探针或验收记录；测量锚点为 `main` `84a151d4f38c` 加 EKB-042 分支（PR #69，`dcb43ad`），宿主版本固定 DSH 0.2.0-rc.2、Codex CLI 0.156.1、Claude Code 2.1.282，机器 Windows 11 26200 / PostgreSQL 16.15。`ErrorEvent v1` 23 个字段逐行 × 四宿主列（✅ 结构化 / 🟡 核心派生 / ⬜ 不可得 / ❓ 未测）：通用 CLI 通道 B（无诊断前回传）、DSH 通道 A（有，headless 会话日志验证）、Codex 通道 B + MCP（无）、Claude 通道 A+B+MCP（有，`SessionStart` + `mcp__errkb__err_*`）；降级表写明字段损失（Codex 回退 C 丢 `tool_name`，DSH 回退 B 丢会话与工具事件，Claude 回退 B 丢 `tool_name` 与回传，日志回退丢退出码）。缺口与影响、未验证清单、测量来源全部写明：DSH `tools/result` 仅单测、Codex `PostToolUse` 未测、模板 C 未接线、模板 E 未单独验收、`shell`/`runtime_versions`/`error_code` 按宿主如实标 🟡/⬜；明确不以窗口标题为能力依据（§3.2）。四宿主闭环记录齐备：通用 CLI `docs/e2e-cli-loop.md`、DSH（EKB-039 实测）、Codex `6a14be33…`、Claude `28c2ad66…`。本卡不改代码（落点仅矩阵与计划行） |

P5 门槛：通用 CLI、DSH、Codex 和 Claude Code 各有一次真实的“错误 -> 查询 -> 解法 -> outcome”录制验收。

### P6 完成 Windows 产品界面

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-044 | 实现托盘和采集状态 | 028、034 | 已完成（2026-10-20）：`internal/ui/status.go` 复用 EKB-028 健康文档（`service.Health`），不另建判定；`classify` 按优先级映射四态（数据库未连接→故障、`queue.backlog > 0`→积压、否则运行），暂停意图与探测结果分开记录。`internal/ui/tray.go`（Windows）注册消息窗口与 `Shell_NotifyIconW` 16×16 圆点图标（绿/灰/红/琥珀），`SetTimer` 每秒重新探测并刷新 Tooltip；菜单含暂停/恢复/重试（故障态显示 Retry）、禁用中的搜索（EKB-045）与设置（EKB-047）、退出；窗口回调 `recover` 隔离 panic，UI 异常不退出守护进程。`cmd/errkb/ui.go` 让 `errkb ui` 同进程托管守护进程：暂停调用 EKB-028 `Daemon.Shutdown`（耗尽在途请求、关闭连接池、释放实例互斥），恢复重新 `StartDaemon`，退出在 10 秒超时内优雅停止。实测（Windows 11 26200 / PostgreSQL 16.15，真实守护进程）：运行 `state=running detail="capturing failures · 1 connection(s) · 0 in flight · queue 0"` 且 Named Pipe 可连；暂停后 `state=paused`，Pipe 报 “the system cannot find the file specified”，采集确实停止；恢复后回到 running 且 Pipe 可连；积压 `state=queue-backlog detail="3 event(s) waiting to replay…"`；数据库故障 `state=database-fault`，详情含 “actively refused”；`RunTray` 在 3 秒后返回 `err=<nil>`，退出无残留锁。测试：`internal/ui/status_test.go` 覆盖四态、暂停停服务、恢复、故障原因、探针 panic 降级、从不缓存；`internal/ui/tray_test.go` 覆盖四态菜单、颜色、图标创建；`cmd/errkb/ui_test.go` 覆盖 spool 计数；`go test ./...` 全绿（database 78.0s）。偏差：新增两个测试文件、`cmd/errkb/ui_test.go` 和 `cmd/errkb/main.go` 的一行分派/用法（§11.1 严格三文件不含测试与子命令接线） |
| ☑ | EKB-045 | 实现浏览器式 omnibox | 024、044 | 已完成（2026-10-21）：`internal/service/search_api.go` 的 `SearchService` 只调用 EKB-024 `database.Retrieve`，不新增检索；`ClassifySearchInput` 先认编号（UUID、12 位指纹、`E-nnnn`）再认错误码、命令、文本，`E-nnnn` 单独回答——数据库只存 UUID，展示编号只存在于导出 Markdown，因此给解释而不是模糊结果，`E-001` 不会命中 `E-0012`。`internal/ui/omnibox.go` 是可测模型：上下键钳制不环绕、Enter 打开、Esc 关闭、输入即清空旧结果、列表硬上限 3、近似行用琥珀色和 `APPROXIMATE — confirm before reuse` 徽标、空态写明 `errkb record` 或 `errkb run -- <命令>`，`displayReason` 把匹配字段译成人话（“exact fingerprint (same failure)”“same error code”“text similarity”）；`internal/ui/omnibox_win.go` 是 Win32 窗口（懒加载 user32/gdi32、`WM_TIMER` 刷新、AsyncSearch 单飞、Alt+Space 切换、Esc 关闭），`internal/ui/tray.go` 菜单加 “Search…” 行，`cmd/errkb/ui.go` 用 `searchBridge` 把 `SearchService` 交给托盘。实测：`go test ./internal/ui/ ./internal/service/` 绿（omnibox 模型测试 + SearchService 集成测试用真实 PostgreSQL 覆盖编号、错误码、命令、文本四类输入）；`go test ./...` 全绿（ui 1.3 s、service 9.8 s、database 55.9 s）；隐私扫描 0 命中。未验证：Win32 窗口的绘制、键盘与热键只能本机人工检查——CI 的 `go` job 在 Linux 上跳过 `internal/ui` 的 Windows 文件（与 EKB-044 同一边界） |
| ☑ | EKB-046 | 实现问题详情和解法编辑 | 021、045 | 已完成（2026-10-21）：`internal/service/edit_api.go` 的 `DetailService` 从 PostgreSQL 读详情——issue 摘要、occurrences（采集时的 os/shell/runtime/命令/标题）、解法及**完整版本历史**（`solution_versions` 只追加，`loadVersions` 按版本降序返回，旧版本正文不被覆盖）、outcome 和 EKB-021 `errkb_trust` 计算的信任度（只读展示，编辑器没有任何写信任的方法）。`AddVersion` 在一个事务里追加版本并写 `solution.version.added` 审计；`ChangeSolutionStatus` 只允许 EKB-021 状态机内的转换，写 `solution.status.changed`（detail 含 from/to），把解法改为 `deprecated`/`blocked` 这类影响注入的转换必须 `confirmed=true`，未确认返回 `ErrConfirmationRequired` 且不改状态、不写审计；`MarkIssueMisjudged` 同样需确认，写 `issue.status.changed`；`DeleteSolution` 需确认，级联删除版本并写 `solution.deleted`；审计行带 operator=`owner`、source=`ui`、detail。`internal/ui/detail.go` 是可测页面模型：摘要行、occurrence 行、按环境分组（分组键只含 os 与 shell，因为运行版本随 occurrence 变化而 outcome 不带运行版本）、版本历史行（逆序、含 source/风险/适用范围）、outcome 行（成功绿/失败琥珀）、`CopyVersion` 只输出正文+出处+环境+风险（风险缺失写 `(not recorded)`，并注明不自动执行）。所有影响注入的修改先进入 `PendingChange`，只有 `Confirm` 会调用 `ChangeApplier`；`Cancel` 丢弃，错误写进状态栏而不是静默吞掉。测试：`internal/ui/detail_test.go` 6 个用例（历史不覆盖、环境分组与成功/失败归属、复制载荷、确认/取消流程、禁止执行/写信任的反射断言、行种类齐全）；`internal/service/edit_api_test.go` 4 个用例（真实 PostgreSQL：追加并审计两次编辑、确认门禁与 from→to 审计、outcome 计入信任但信任不可写、缺失参数与未知 id 的错误码）；`go test ./...` 全绿（ui 8.5 s、database 69.2 s、service 21.2 s）；隐私扫描 0 命中。偏差：新增两个测试文件（§11.1 严格三文件不含测试）；本卡只交付可测模型与编辑服务，Win32 详情窗口未接线（`omnibox_win.go` 里仍写着详情页随 EKB-046 到达，接线留下一个窗口卡；无 UI 按钮会自行执行修复）；`service.VersionInfo` 增加 `SolutionID` 字段，让“给已有解法追加版本”能从界面模型连回解法。边界：信任只展示、不写入；outcome 只能通过 EKB-021 既有路径记录，编辑器不代替用户宣称成功 |
| ☑ | EKB-047 | 实现连接器和隐私设置 | 007、043-046 | 已完成（2026-10-21）：`internal/ui/settings.go` 是设置页模型并**复用** EKB-007 的 `contract.LoadConfig`/`contract.Config`——页面不持有第二个配置结构，所有读写都走这一个模型；`FileSettingsStore.Save` 先 `Config.Validate` 再用 `contract.LoadConfig` 复核落盘内容，非法改动不可能替换掉合法文件（`commit` 里也校验，假 store 同样挡得住）。连接器列表来自 `connectors` 表并带 channel 等级（EKB-043）与最近活跃时间（`events.observed_at`，无事件时退回 `created_at`）；`SetConnectorEnabled`/`SetProjectEnabled`/`SetCaptureEnabled` 在保存成功后立刻让 `AllowsCapture` 返回 false（全局暂停同时关闭 `notices.before_diagnosis`，否则契约会拒绝该组合，恢复时一并打开）；保留期是 samples/normalized/audit 三个独立输入；删除按项目、按连接器、按时间选择，先 `PreviewDelete` 逐表显示将删除的 occurrence/event/issue/版本/outcome/retrieval/notice 条数，`ConfirmDelete` 才执行（未预览直接拒绝），执行后写 `actor=settings` 的 `delete.scope` 审计行，若实际计数与预览漂移会提示重看；HTTP 默认关，开启必须填 ≥32 字符 token 且只允许回环（UI 层先拒，契约层再保证），Named Pipe 状态单独可读。删除与保留直接复用 EKB-026 的 `PreviewDelete`/`DeleteScoped`/`ApplyRetention`，不另写 SQL。实测：`go test ./internal/ui/` 全绿（15 个设置页用例 + 真实 PostgreSQL 数据源用例）；`go test ./...` 全绿（ui 3.1 s、database 52.9 s、service 14.1 s）；`gofmt`/`go vet`/`cue vet ./spec/...` 干净；隐私扫描 0 命中；把设置页改过的配置导出 JSON 并用 `#Config` 统一后 `cue vet` 通过，负例（`retention.sample_days = -1`）被 CUE 正确拒绝（配置往返证据）。偏差：新增 `internal/ui/settings_test.go`（卡内期望输出 3 要求测试，但 §11.1 落点只写 `settings.go`；与 EKB-030/034/045/046 同样处理）。边界：`AllowsCapture` 是页面模型的即时闸门；把它接到真实采集入口（`SQLBackend`/daemon）需要运行时配置提供者，按 §11.1 严格落点不在本卡，留待接线卡，本卡只保证页面侧“点停用即刻拒绝新采集”的语义与测试 |
| ☑ | EKB-048 | 实现通知和一键复制 | 024、044-047 | ✅ 已完成：`internal/service/notifications.go` 在 EKB-024 检索之上组装通知——top 1–3 硬上限（只保留有解法的候选）、精确/近似徽章、可信度与样本量、`environment_diff` 非空时输出 may-not-apply 警告；正文尾行固定为 `(estimated size: about N tokens — an estimate, not an exact count)`；内容与估算写入 `notices`（`kind=desktop`），无解法时不写、同 issue 2 分钟抑制窗口内重复触发只返回 `Suppressed` 不重复落库（进程内最多 256 条）。`internal/ui/notify.go` 是非模态模型：`Modal()/StealsFocus()/Executes()` 恒 false，导出面经反射测试锁定为视图/选择/复制/打开，无执行入口；`CopyText` 含来源、适用环境、风险与 issue id。实测：`go test ./internal/ui/ ./internal/service/` 全绿（9 个 UI 模型测试 + 4 个服务测试，含真实 PostgreSQL 集成——notices 落 1 行且 `estimated_tokens` 与返回估算一致、同 issue 第二次 `suppressed`、无解法 0 行）；`go test ./... -count=1` 绿（ui 1.5 s、service 16.2 s、database 66.7 s）；`gofmt -l .` 空、`go vet ./...` 干净、隐私守卫 0 命中。偏差：按惯例新增 `internal/ui/notify_test.go` 与 `internal/service/notifications_test.go`；Win32 吐司与剪贴板属宿主界面层，本卡交付可测试的非模态模型与复制文本，真实弹窗由界面卡接线。边界：抑制状态是进程内的，重启后同 issue 会再次提示 |
| ☑ | EKB-049 | 实现统计和审计视图 | 021、048 | ✅ 已完成（2026-10-21）：`internal/audit/log.go` 是审计写入/读取 API——`Record` 先校验 action/entity/operator/source，`detail` 以 jsonb 落库；`Query` 支持时间范围、动作、实体和对象 id 四类过滤（页大小默认 50、上限 200），`Actions` 提供过滤下拉。`internal/audit/stats.go` 用 PostgreSQL 聚合出 §13 的指标：检索轮次与精确 top-1 命中（按 session/query/秒聚合成一轮）、近似 top-1/top-3 准确率（以之后出现的 success outcome 作为“被确认”）、**误合并计数（目标必须为 0）**、`percentile_cont` 算出的检索 p50/p95（新迁移 `db/migrations/0008_retrieval_latency.sql` 给 `retrievals` 加可空 `duration_ms`，`Retrieve` 实测写入）、解法 success/failure/unknown 与成功率、`notices` 的注入字符与估算 token、重复失败（`issues.hits > 1`）、捕获到通知的延迟、人工修正（`solution.*` 与 `issue.status.changed` 审计动作）以及 `fault.*` 持久故障计数；节省拆成实测（注入字符、按字符估算的 token）与估计（避免的重复诊断 = 有已验收解法的 issue 的重复命中数，token 按公开常量 `AssumedTokensPerDiagnosis = 400` 估算，假设原文由视图展示）。`internal/ui/stats.go` 是只读视图模型：误合并行永远存在且标注 target 0，p50/p95 标注 measured，节省行逐条标 measured/estimated 并附假设，检索轮次少于 5 时提示比率尚无意义，审计列表按时间/动作/对象显示并带分页。实测：`go test ./internal/audit/ ./internal/ui/` 绿（审计 API、真实 PostgreSQL 聚合用例、6 组 UI 模型用例）；`go test ./... -count=1` 全绿（audit 2.8 s、database 63.2 s、service 15.8 s、ui 1.7 s）；`gofmt -l .` 空、`go vet ./...` 与 `cue vet ./spec/...` 干净；隐私扫描 0 命中。偏差：新增 `internal/audit/stats_test.go` 与 `internal/ui/stats_test.go`，并回填 `0008` 迁移与 `internal/database/rerank.go` 的计时写入——§11.1 落点只写三个文件，但“延迟必须实测”“误合并必须可见”都需要数据来源，按 EKB-034/046 的先例记录。P6 状态：EKB-044 至 EKB-048 全部 ☑；托盘和 omnibox 已接通，设置/详情/统计为可测试模型，最终 Win32 窗口接线随 EKB-050 落地。边界：捕获到通知延迟只统计有 issue 的通知；近似准确率以“之后有 success outcome”为确认口径，是下界而非真值；节省数字是假设驱动的估计，视图不支持把它当实测展示 |

P6 门槛：用户不需要打开数据库或终端，就能暂停采集、搜索、编辑、反馈和审计一次复用过程。

### P7 打包 发布和维护

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☑ | EKB-050 | 构建 Windows 发布二进制 | 035、049 | ✅ 已完成（2026-10-21）：`cmd/errkb/version.go` 定义 `version`/`commit`/`buildTime` 三个可由 `-ldflags -X` 注入的变量，并输出与 `versioninfo.json` 对应的版本行；`packaging/windows/versioninfo.json` 是产品元数据的唯一来源（product/description/copyright）；`packaging/windows/build.ps1` 做真实构建：`go-winres simply` 按该元数据生成资源（版本信息 + CLI manifest），`go build -trimpath -buildvcs=false` 配空 build id 的 `-ldflags` 注入版本、提交和构建时间（构建时间取 HEAD 的提交时间戳，不取墙钟），`CGO_ENABLED=0`（纯 Go、目标机不需要 C 运行时；但发布物仍需 PostgreSQL 和连接器，不承诺免依赖），产物为 `dist/errkb.exe`、`dist/errkb.exe.sha256` 和解析后的 `dist/versioninfo.json`，任何步骤失败脚本非零退出。实测：同参数连续两次构建的 SHA256 完全一致（`0634561e…`）；`errkb version` 输出 `errkb 0.1.0` / `commit abc1234` / `built 2026-10-21T00:00:00Z` / `go go1.26.4 windows/amd64`，与 `dist/versioninfo.json` 的 version/commit/build_time 逐项一致；PE 资源读回 `ProductVersion/FileVersion = 0.1.0`，FileDescription/OriginalFilename/LegalCopyright 与元数据一致；把 PATH 缩到只有 System32 后同一二进制仍能启动并打印版本，运行不依赖 Go 工具链。偏差：`cmd/errkb/main.go` 只改 `version` 分支为调用 `printVersion` 并删掉重复的版本变量（§11.1 只列三个文件，按 EKB-034/046/049 先例记录）；构建期依赖 `go-winres`（`go install github.com/tc-hib/go-winres@latest`），运行期不需要。边界：可复现范围是“相同源码 + 相同参数 + 相同 Go/go-winres 版本”，两者版本需在 EKB-054 流水线中钉住；安装器、签名、SBOM 归 EKB-051/054；`doctor` 的退出码语义不在本卡范围 |
| ☑ | EKB-051 | 实现 PostgreSQL 安装和生命周期 | 004、027、050 | ✅ 已完成（2026-10-21）：`internal/service/pg_manage.go` 覆盖六动作——`Install`（initdb、`crypto/rand` 32 字节密码、启动、建库、按 `MigrationsDir` 迁移、写 0600 的 `credentials.json`）、`Start`/`Stop`（`deleteData` 显式选择）、`Backup`（`pg_dump -Fc` + `database.BackupManifest`）、`Upgrade`（升级前自动备份，迁移失败时 `restoreInPlace` 回滚并重报错误）、`Uninstall`（`deleteData` 选择；清理进程、服务、端口、目录）、`Adopt`/`Connect`/`Health`/`Discover`（数据目录、端口、服务与已装实例冲突检测，识别可用于认领的外部实例），密码只用 `crypto/rand` 生成、只存 `credentials.json`（受限 ACL），无默认口令。`packaging/windows/postgres.ps1` 只编排实例动作（install/start/stop/uninstall/status），无业务逻辑；postmaster 经 WMI `Win32_Process.Create` 由 `cmd` 重定向到日志文件启动——普通 `pg_ctl`/`Start-Process`/`cmd /c start` 都会把调用方的标准句柄泄漏给常驻服务，管道式调用会无限等待 EOF；就绪判定用 `pg_isready`，不只轮询端口。`packaging/windows/installer.iss` 含数据保留页、已有实例/端口冲突三选一（新建专用实例/复用现有实例/换端口）与卸载时「保留还是删除数据」的确认，PostgreSQL 缺失时安装器拒绝启动。实测（2026-10-21）：PS 流程全绿——随机端口 51424 安装后 `status` 报 installed/listening/PostgreSQL 16，重复 install 以「cluster already exists」退出 1，stop 幂等（再次 stop 输出 already stopped），start 恢复监听，uninstall 保留数据（`PG_VERSION` 仍在、凭据删除、端口释放），uninstall `-DeleteData`（目录删除、端口释放、0 个 errkb 服务、0 个 postgres 残留进程）；`go test ./internal/service/ -run TestPGManager` 6/6（安装+健康+备份+卸载 7.97s、保留数据 7.04s、忙端口拒绝 0.04s、升级迁移失败回滚 8.41s、无新迁移空操作 7.01s、服务注册/验证 0.00s），全套 `go test ./... -count=1` 绿（service 46.4s）；`errkb doctor --dsn <凭据 DSN>` 对刚装实例输出 `database: PostgreSQL 16.15, database errkb, 6 connection(s)`，实例状态可见。偏差：新增 `pg_manage_test.go`、`pg_manage_internal_test.go`（§11.1 仅列三文件，按 EKB-034/046/049 先例记录）；`installer.iss` 由 EKB-052 首次编译并修复两处缺陷（集群页顺序、PowerShell 5.1 随机数 API）。边界：PS 只建空应用库，schema 迁移属应用侧（`PGManager.Install` 带 `MigrationsDir` 会迁移；`errkb` 尚无 `db migrate` 子命令，留给 EKB-052/053）；doctor 的实例可见性由 database 检查体现，不是安装器所有权检查 |
| ⚠ | EKB-052 | 完成干净虚拟机验收 | 050-051 | ⚠ 阻塞（2026-10-21）：验收脚本与报告已落盘——`packaging/windows/vm-acceptance.ps1` 分阶段（artifacts / preflight / install / firstrun / loop / rebootprepare / afterreboot / uninstall / all）自动校验，结构化 `results.json` 加每项证据文件，失败非零退出，无法自动化的点击标 `MANUAL`；`docs/acceptance-vm.md` 记录机器规格、两枚产物哈希（安装器 `703B535C540BD99D0B3C2ED1FEB6E706404C130DEF6CB2970E0818196077717F` 4,950,820 字节，`errkb.exe` `f9942ff0cedb3352a59f57dff8633d8fd89fc0832c93313032fce5eb620d002d`，版本 `errkb 0.1.0`）、执行步骤、人工闸门、证据布局、验收映射与边界。本机干跑：`artifacts` PASS（安装器存在、4.9 MB、校验和匹配）、`preflight -AllowAdmin -AllowDirtyHost` PASS（PostgreSQL 16 存在、5433 空闲、未安装）。**干净虚拟机尚未执行**——本环境无 hypervisor/VM 工具（无 `vmrun`/`qemu`/`VBoxManage`，Hyper-V 查询需管理员），无法完成全新系统、普通用户、重启和卸载。构建期间发现阻塞缺陷；安装器的两处真实缺陷已在本卡修复（分页顺序、PowerShell 5.1 随机数 API，属 EKB-051 文件，记录为偏差），其余按卡要求记录不改：(1) 全新安装无法记录错误——`packaging/windows/installer.iss:53-57` 只创建并启动集群，不执行 schema 迁移，CLI 也没有 `db migrate` 子命令（分发仅 `help/version/daemon/ui/doctor/run/mcp`），而 daemon 在 ledger 为空时拒绝启动（`errkb daemon: schema migration check failed: applied version "", expected "0008" (run \`errkb db migrate\` first)`），首次 `errkb run` 即失败；(2) 重启后无自启动——安装器未调用 `PGManager.RegisterService`，daemon/托盘也没有开机启动，重启后端口关闭、doctor 报数据库连接拒绝。两项修复需另开卡（CLI 迁移子命令或安装器迁移步骤；集群与 daemon 服务化），并在同一干净机上复测；脚本已把卸载保留/删除、残留检查、MCP 记录/反馈闭环写成自动断言。本机彩排（2026-10-21，非管理员开发机）：本轮首次实际编译并运行安装器，暴露两处缺陷——集群页建在目录页之前，静默模式在 `{app}` 初始化前就展开常量（改到 `wpSelectDir` 之后创建）；`postgres.ps1` 用 .NET 6+ 的 `RandomNumberGenerator.Fill`，安装器跑在 Windows PowerShell 5.1 下直接失败（改用 `Create()`+`GetBytes()`）。修复后静默安装退出 0：`errkb.exe`/`postgres.ps1`/`README.md`/卸载器落到 `%LOCALAPPDATA%\errkb`，5433 集群监听，`pg\credentials.json` 生成。手动迁移后闭环实测：`errkb run --connector cli --session ekb052 --project probe -- cmd /c "exit 9"` 记录 issue `a2a91bd8-a30c-4c64-b353-020a8696a52e`（指纹 `4202fb44db19`），`search` 命中，MCP `err_record_solution` 建版本 `16374da4-a2f1-4a23-8938-83661786bfe7`，第二次 `run` 打印 fix（source cli / trust 0.50 / version），MCP `err_feedback success`，`stats` 报 `events=2 issues=1 solutions=1 outcomes=1`。迁移缺口与自启动缺口仍在，故仍为阻塞 |
| ☑ | EKB-053 | 建立升级和兼容测试 | 011、052 | ✅ 已完成（2026-10-21）：升级测试用提交在仓库里的真实旧产物，不靠临时编造——`db/migrations/testdata/upgrade/` 存五份夹具并各自附来源注释：`schema-0007.sql`（PostgreSQL 16.15 `pg_dump --schema-only`，含迁移器 ledger 的真实 SHA-256，来自 `84f8895`）、`config-v0.yaml`（`5ad354d` 在可选 `templates` 块出现前由 `yaml.Marshal` 生成）、`ERRORS-v0.md`（`aad0835` 的 TypeScript 导出器产物，3 条种子）、`ERRORS-history.md`（`ExportMarkdown` 生成，含两个解法版本与 `errkb-history: v1` 历史注释）、`capability-v0.json`（`fcc01eb` 的 claude-code 能力文件）。`spec/compat.cue` 新增 `upgrade` 块：schema/config/Markdown/connector 四类承诺、六行版本矩阵（可直升 / 升级失败回滚 / 更高版本明确拒绝）和夹具来源清单，把“可直升/需中转/不支持”写成机器可读事实。`internal/database/upgrade_test.go` 七个测试：0007→0008 行数与内容不变、旧 retrieval 的 `duration_ms` 保持 null（不把“没测过”读成 0）、升级后 `Retrieve` 写入实测毫秒数且新事件仍可记录；迁移中途失败整事务回滚（`broken_probe` 不存在、ledger 停在 0008、行数不变），修好文件重跑只补缺失版本；ledger 出现更高版本（`9999`）时拒绝打开；旧配置字段逐项保留、`templates` 为 nil、未知键 `future_option` 被点名拒绝；旧 Markdown 导入/导出指纹不变、多版本按版本序保留两条、重复导入新增 0 个版本、被污染的导入文本落库后无明文密钥且出现 `<secret>`；旧能力文件加载后 `Degradation` 点名 `error_code` 与 fallback B，v2 协议与未知通道均报错拒绝。实测：`go test ./internal/database/ -run 'TestUpgrade\|TestOld' -count=1` 7/7 绿（约 2.8s）；全套 `go test ./... -count=1` 绿（database 57.5s、service 49.3s、e2e 3.9s）；`gofmt`/`go vet`/`cue vet` 干净；隐私扫描 0 命中。偏差：新增夹具目录与测试文件（§11.1 原只列三处，按 EKB-034/046 先例记录）、`spec/compat.cue` 回填、`.prettierignore` 新增 `db/migrations/testdata/`（历史产物不可被格式化改写）、`.github/workflows/ci.yml` 的 database job 迁移循环改为 `find -maxdepth 1`（夹具不是迁移，否则 CI 会把 0007 dump 当迁移重放而失败）。边界：只保证“向后读写旧产物”；不做自动降级，schema/协议版本高于本二进制时明确拒绝；夹具是快照，重生成需要那些历史提交 |
| ☑ | EKB-054 | 建立发布工作流 | 050-053 | ✅ 已完成（2026-10-21）：`packaging/windows/release.ps1` 分 `-Mode check|package` 两种模式。check 依次校验：版本是 SemVer、标签必须等于 `v<version>`、`CHANGELOG.md` 必须有 `[version]` 小节、干净虚拟机验收版本（`VM_ACCEPTANCE_VERSION`，无记录直接拒绝）、`installer.iss` 的 `#define AppVersion` 与版本一致；任一不符抛错且退出码 1。package 先重复 check，再调 `build.ps1`（提交戳默认 `git rev-parse --short HEAD`、构建时间默认提交时间戳），核对二进制 `errkb version` 的 version/commit/built 三行与发布参数一致，然后从零重建 `dist/release/errkb-<version>/`：`errkb.exe`、`errkb.exe.sha256`，可选 `errkb-<version>-setup.exe`；`sbom.json`（SPDX 2.3 子集：从 `go version -m` 读出的全部 Go 模块、工具链 Go/go-winres、PostgreSQL 运行前置、Inno Setup；`distributed` 字段区分随附与不随附）；`THIRD-PARTY-NOTICES.md`（逐模块 SPDX 标识 + PostgreSQL License 全文 + Inno 说明）；`SIGNING.md`（未签名事实、原因、校验和作为完整性边界、风险与计划）；`RELEASE-NOTES.md`（`CHANGELOG.md` 对应小节）；`manifest.json`（tag/commit/build_time/toolchain/可复现范围/各产物 sha256）与 `SHA256SUMS`（排序后覆盖除自身外全部文件）。；实测：同 tag+commit+构建时间连续两次打包（8 个产物、不含安装器）逐文件 SHA256 一致、`SHA256SUMS` 哈希一致；`errkb.exe` 10,897,408 字节；`-IncludeInstaller` 构建出 `errkb-0.1.0-setup.exe`，`ProductVersion=0.1.0`、`CompanyName=jingchangzhao-gif`；五种负例（无 VM 记录、标签不符、changelog 缺小节、VM 版本不符、安装器版本不符）均非零退出；`CHANGELOG.md` 与 `release.yml` 通过 prettier，YAML 可解析（jobs: release），三个改动文件隐私守卫 0 命中。偏差：安装器不是字节级可复现（Inno Setup 在 setup.exe 内嵌编译时间），`manifest.json` 的 `reproducibility.installer` 明确记录差异来源与验证方法（安装后比对 `errkb.exe.sha256`），可复现承诺只覆盖二进制与生成文档；本卡未推送 tag、未真正发布（本机无向远程推送权限；release.yml 需要 tag），流水线用 `VM_ACCEPTANCE_VERSION` 仓库变量把关，未设置即拒绝发布。边界：`gh release create/upload` 依赖 `GH_TOKEN`（CI 提供）；签名与时间戳留待有证书后处理；EKB-052 的干净机验收仍是 ⚠ 阻塞，发布门槛按卡要求保持拒绝 |
| ⚠ | EKB-055 | 收口中英文文档 | 054 | ⚠ 文档已完成并验证，P7 收口待 EKB-052 干净虚拟机验收（2026-10-21）：两份 README 按同一章节结构重写并逐节对应（这是什么、它怎么工作、支持的主机（实测）、核心保证、快速开始、命令行、配置、系统要求、兼容性与版本口径、安装与卸载、隐私、文档索引、已知局限、开发、参与贡献、许可证）；能力声明逐行指向实测来源（CLI：`docs/e2e-cli-loop.md`；DSH：EKB-039、PR #66；Codex：`connectors/codex/probe.md`；Claude Code：`connectors/claude/probe.md`），未实测项按 `docs/connector-matrix.md` 的 Unverified 清单如实标注；新增 `docs/install.md`（系统要求、交互/静默安装、数据位置、daemon 参数、复用实例、pg_dump 备份、升级、卸载保留/删除、故障排查）与 `docs/privacy.md`（数据流、脱敏单一来源、默认 Named Pipe 与 HTTP 关闭、三类保留期及当前手动执行的限制、带预览与审计的删除、离线队列原始观测的边界）；删除 `README.toolbox.md`、`README.toolbox.zh-CN.md`（经确认，内容已被主 README 与计划覆盖）；版本口径在 README 写清 rc.2 → rc.3（peer `^0.1.5-rc.2` 覆盖 rc.3、适配器 API 面不变）与 DSH 宿主 0.2.x（0.2.0-rc.2 实测，`dsh-ops` peer 为证）；无 P0/§17/T01–T18 残留；与 EKB-052 实测一致（安装目录、5433 专用实例、首次运行缺迁移、无自启均写明）；Contents 锚点全部自检通过，两份 README 章节数一致；未改代码；P7 收口仍受 EKB-052 ⚠ 阻塞：EKB-050–054 中 052 尚未 ☑，根因是干净虚拟机硬件缺失，与文档无关；项目和仓库已改名为 Error Knowledge Base（`error-knowledge-base`）；命令 `errkb` 与 DSH 插件包 `dsh-errkb` 保持不变，DSH STORE 条目待审完后再按新仓库地址重新提交 |

P7 门槛：干净 Windows 虚拟机可以安装、升级、备份、卸载并保留或删除数据；发布物有校验和和可追踪版本。

### 11.1 按卡的文件施工顺序

本表按卡号顺序列出每张卡要落地的文件，`→` 表示先后。全部路径相对仓库根目录；同一文件在后续卡再次出现，表示继续修改而不是重建。P0 的 002、003、004 在 001 之后可并行，005 等三者全部验收；P5 的 040 与 041 可并行，但都必须在 036、037 之后。

| 卡 | 文件（按顺序） |
| --- | --- |
| EKB-001 | 无文件改动（克隆仓库、带入本文件与英文版、记录 HEAD） |
| EKB-002 | `package.json`、`pnpm-lock.yaml`（冻结版本口径）；Go、PostgreSQL、CUE 的本机版本记入本文件 |
| EKB-003 | `src/store.ts` → `tests/store.test.ts` |
| EKB-004 | 本文件第 2 节（数据库分发方式的书面决定，不产生代码） |
| EKB-005 | `go.mod`、`go.sum` → `cmd/errkb/main.go` → `internal/{contract,core,capture,database,service,mcp,ui,audit}/doc.go` 空包 → `spec/{error-event,config,mcp-tools}.cue` 空 schema → `db/migrations/0001_init.sql` 空壳 → `packaging/windows/build.ps1` 空壳 → 改 `.github/workflows/ci.yml`、`.gitignore`、`.gitattributes` |
| EKB-006 | `spec/error-event.cue` → `internal/contract/error_event.go` → `internal/contract/error_event_test.go` |
| EKB-007 | `spec/config.cue` → `spec/connector-capability.cue` → `internal/contract/config.go` → `internal/contract/config_test.go` |
| EKB-008 | `spec/mcp-tools.cue` → `spec/local-api.cue` → `internal/contract/tools.go`、`internal/contract/local_api.go` |
| EKB-009 | `tests/gen-corpus.ts` → `spec/corpus/{normalize,headline,redact,fingerprint,tokenize,match,notice}.json` → 六个既有测试文件增加「读语料断言」，不删原断言 |
| EKB-010 | `spec/corpus/labelled/{same-error,distinct-lookalike,cross-language}.json` → `spec/corpus/labelled/README.md` |
| EKB-011 | `spec/compat.cue` → `.github/workflows/ci.yml` 增加兼容 job |
| EKB-012 | `internal/core/headline.go` → `internal/core/headline_test.go` |
| EKB-013 | `internal/core/normalize.go` → `internal/core/fingerprint.go` → 两个测试（对齐 `normalize.json`、`fingerprint.json`） |
| EKB-014 | `spec/redact.cue`（唯一来源）→ `tests/gen-redact-rules.ts` → `internal/redact/rules_gen.go`、`src/redact-patterns.ts`（改为生成物）、`privacy-guard.yml` 规则校验 → `internal/redact/redact.go` → `rules_gen_test.go`、`redact_test.go` |
| EKB-015 | `internal/core/tokenize.go` → `internal/core/jaccard.go` → 对应测试 |
| EKB-016 | `internal/core/classify.go` → 对应测试 |
| EKB-017 | `internal/core/rerank.go` → 对应测试 |
| EKB-018 | `internal/core/diff_test.go`（逐字段比对语料）；有意差异记入 `spec/corpus/divergences.md` |
| EKB-019 | `db/migrations/0001_init.sql`（空壳换成第 4 节实体）→ `internal/database/migrate.go` → `internal/database/migrate_test.go` → `db/migrations/README.md` |
| EKB-020 | `db/migrations/0002_record.sql`（`errkb_record_event()`）→ `internal/database/record.go` → `internal/database/record_test.go`（50 并发） |
| EKB-021 | `db/migrations/0003_solutions.sql`（`errkb_trust()`）→ `internal/database/solution.go` → `internal/database/outcome_test.go` |
| EKB-022 | `db/migrations/0004_exact_search.sql` → `internal/database/search.go` → `internal/database/search_test.go` |
| EKB-023 | `db/migrations/0005_trigram.sql`（`pg_trgm` + GIN）→ `internal/database/candidates.go` → 基准测试 |
| EKB-024 | `internal/database/rerank.go`（消费 `match.json`）→ 测试（零误合并门槛） |
| EKB-025 | `internal/database/markdown.go` → `internal/database/markdown_test.go`；`seeds/ERRORS.seed.md` 只读，生成 `seeds/*.json` |
| EKB-026 | `db/migrations/0006_retention.sql` → `internal/database/backup.go` → 恢复测试（空库恢复） |
| EKB-027 | `db/migrations/0007_occurrence_lookup.sql` → `internal/database/candidates.go`（trigram 候选输入 64 字符上限）→ `internal/database/bench_test.go`（10 万 issue 数据集）→ 查询计划记入 `docs/benchmarks.md` |
| EKB-028 | `cmd/errkb/daemon.go` → `internal/service/daemon.go` → `internal/service/daemon_test.go` |
| EKB-029 | `internal/service/pipe.go` → `internal/service/api.go` → `internal/service/pipe_test.go` |
| EKB-030 | `cmd/errkb/main.go`（子命令分派）→ `cmd/errkb/cli.go` → `internal/service/client.go` → `cmd/errkb/cli_test.go` → `internal/service/client_test.go`（测试文件为本卡「期望输出」所需，超出原三文件行；见计划状态行偏差） |
| EKB-031 | `internal/capture/run.go` → `internal/capture/run_test.go` |
| EKB-032 | `internal/capture/tail.go` → `internal/capture/tail_test.go` |
| EKB-033 | `internal/service/queue.go` → `internal/service/queue_test.go`；`src/state.ts` 只保留连接器本地队列（该缩减按所有者决定顺延到 EKB-038） |
| EKB-034 | `cmd/errkb/doctor.go` → `internal/service/doctor.go`（另加 `doctor_test.go` 两个与 `cmd/errkb/main.go` 分派，偏差见行）；§11.1 的生产文件之外只动计划行 |
| EKB-035 | `tests/e2e/`（真实命令场景脚本）+ 演示记录 `docs/e2e-cli-loop.md`；回填：`spec/mcp-tools.cue`/`internal/contract/tools.go` 的 `Candidate.solution`、`internal/database/rerank.go` 解法附着与 `internal/database/overview.go`、`internal/service/backend.go` 与 `daemon.go` 的管道服务、`cmd/errkb/run.go` 及 `cmd/errkb/main.go` 分派（偏差见行） |
| EKB-036 | `spec/connector.cue` → `internal/capture/connector.go` → 能力探针测试 |
| EKB-037 | `internal/mcp/server.go` → `internal/mcp/tools.go` → `internal/mcp/tools_test.go` → `cmd/errkb/mcp.go`（另改 `cmd/errkb/main.go` 的 `mcp` 分派与用法行，偏差） |
| EKB-038b | `spec/mcp-tools.cue` → `spec/local-api.cue` → `spec/compat.cue` → `internal/contract/tools.go` → `internal/contract/local_api.go` → `internal/service/api.go` → `internal/service/backend.go` → `internal/database/solution.go` → `internal/database/solution_record.go` → `internal/mcp/tools.go` → `internal/service/client.go` → `cmd/errkb/cli.go` → `cmd/errkb/mcp.go` |
| EKB-038 | `src/plugin.ts` → `src/tools.ts` → `src/index.ts` → `tests/plugin.test.ts`（断线与恢复） |
| EKB-038c | `src/connector.ts` → `src/state.ts`（队列追加）→ `src/index.ts`（re-export）→ `tests/connector.test.ts` |
| EKB-038d | `src/plugin.ts` → `src/tools.ts` → `src/index.ts` → `src/state.ts`（只留队列）→ `tests/support.ts` → `tests/plugin.test.ts` → `tests/tools.test.ts` → `tests/injection.test.ts` → `tests/state.test.ts` → `tests/settings.test.ts`；回填 `spec/mcp-tools.cue` → `spec/compat.cue` → `internal/contract/tools.go` → `internal/database/rerank.go` → `internal/database/overview.go` → `internal/service/backend.go`（可选 `hits`/`status`/`fix`）→ 对应 Go 测试 |
| EKB-039 | `src/plugin.ts`（瀑布监听、providers、去重、feedback）→ `src/index.ts` → `src/tools.ts` → `src/resolve-detect.ts` → `tests/plugin.test.ts` → `tests/tools.test.ts` → `tests/injection.test.ts` → `tests/settings.test.ts` → `tests/support.ts`；回填 `spec/mcp-tools.cue` → `spec/error-event.cue` → `internal/contract/tools.go` → `internal/contract/error_event.go` → `internal/service/backend.go` → 对应 Go 测试 |
| EKB-040 | `connectors/codex/probe.md`（先记录当期能力）→ `connectors/codex/`（README.md、capability.json、hooks.toml、errkb-codex.ps1）→ 回填：`cmd/errkb/run.go`、`internal/service/client.go`、`cmd/errkb/cli.go`、`cmd/errkb/cli_test.go` |
| EKB-041 | `connectors/claude/probe.md` → `connectors/claude/{README.md,capability.json,hooks.json,mcp.json,errkb-claude-hook.ps1,errkb-session-hint.ps1,errkb-claude.ps1}`；回填 `cmd/errkb/run.go`（capture 的 `channel: B`） |
| EKB-042 | `connectors/templates/cli-log/config.yaml` → `connectors/templates/cli-log/README.md`（回填：`spec/config.cue` → `internal/contract/config.go` → `internal/contract/config_test.go` → `spec/compat.cue` → `.github/workflows/ci.yml`） |
| EKB-043 | `docs/connector-matrix.md` → `docs/connector-matrix.zh-CN.md`（同构同步） |
| EKB-044 | `internal/ui/tray.go` → `internal/ui/status.go` → `cmd/errkb/ui.go` → 测试（`internal/ui/status_test.go`、`internal/ui/tray_test.go`、`cmd/errkb/ui_test.go`）；`cmd/errkb/main.go` 加一行分派 |
| EKB-045 | `internal/ui/omnibox.go`（可测模型）→ `internal/ui/omnibox_test.go` → `internal/service/search_api.go` → `internal/service/search_api_test.go` → `internal/service/search_api_integration_test.go`（真实 PostgreSQL）→ `internal/ui/omnibox_win.go`（Win32 窗口，偏差）→ `internal/ui/tray.go` 菜单入口 + `cmd/errkb/ui.go` 桥接（偏差） |
| EKB-046 | `internal/ui/detail.go`（可测页面模型）→ `internal/ui/detail_test.go` → `internal/service/edit_api.go` → `internal/service/edit_api_test.go`（真实 PostgreSQL 与审计断言） |
| EKB-047 | `internal/ui/settings.go` → 复用 `internal/contract/config.go`（不新造配置模型）→ `internal/ui/settings_test.go`（设置页模型 + 真实 PostgreSQL 数据源测试；卡内作用域外，记录为偏差） |
| EKB-048 | `internal/ui/notify.go` → `internal/ui/notify_test.go` → `internal/service/notifications.go` → `internal/service/notifications_test.go` |
| EKB-049 | `internal/audit/log.go` → `internal/audit/stats.go` → `internal/ui/stats.go` → `internal/audit/stats_test.go` → `internal/ui/stats_test.go`；回填 `db/migrations/0008_retrieval_latency.sql` → `internal/database/rerank.go`（写入实测 `duration_ms`） |
| EKB-050 | `cmd/errkb/version.go` → `packaging/windows/versioninfo.json` → `packaging/windows/build.ps1`（真实构建）；`cmd/errkb/main.go` 只改版本分发一行 |
| EKB-051 | `packaging/windows/postgres.ps1` → `packaging/windows/installer.iss` → `internal/service/pg_manage.go`；回填 `internal/service/pg_manage_test.go` → `internal/service/pg_manage_internal_test.go`（测试文件超出本表，按 EKB-034/046 先例记录） |
| EKB-052 | `docs/acceptance-vm.md` → `packaging/windows/vm-acceptance.ps1` |
| EKB-053 | `db/migrations/testdata/upgrade/`（五份真实旧产物）→ `spec/compat.cue`（`upgrade` 块：四类承诺、升级矩阵、夹具来源）→ `internal/database/upgrade_test.go`（七个测试）；回填 `.prettierignore` 排除历史夹具 |
| EKB-054 | `packaging/windows/release.ps1` → `.github/workflows/release.yml` → `CHANGELOG.md`（发布前读取 `docs/acceptance-vm.md` 的验收结论）|
| EKB-055 | `README.md` → `README.zh-CN.md` → 删除 `README.toolbox.md`、`README.toolbox.zh-CN.md` → `docs/install.md`、`docs/privacy.md` → 本文件与英文版逐节同步 |

## 12 公共质量门

每张任务卡按涉及范围运行以下检查，任何失败都不能标记为完成：

```powershell
pnpm install --frozen-lockfile
pnpm build
pnpm typecheck
pnpm lint
pnpm format:check
pnpm test

go build ./...
go vet ./...
go test ./...
gofmt -l .

cue vet ./spec/...
errkb db migrate --check
```

另外必须满足：

- 新行为有自动化测试；修复缺陷有回归测试；
- 所有外部输入在边界校验，所有落盘文本先脱敏；
- 采集、查询和回传失败不能中断宿主；
- 近似匹配不自动合并问题；
- README 不声称尚未通过真实验收的能力已经可用；
- 中英文文档在同一任务中更新；
- 阶段结束时必须运行真实场景演示，而不只运行单元测试。

## 13 度量指标

版本一验收至少持续记录：

- 精确指纹命中率；
- 近似候选 top 1 和 top 3 命中率；
- 错误合并数量，目标必须为零；
- 已验证解法成功率和失败率；
- 同一技术错误重复诊断次数；
- 检索 p50 和 p95；
- 捕获到通知的延迟；
- 返回给宿主的字符数和估算 token 数；
- 手工纠正分类和匹配的次数；
- 数据库、队列和连接器故障次数。

“节省 token”不能作为精确事实。系统只能准确记录自己注入了多少内容，并以明确标注的假设估计避免的重复诊断成本。

## 14 版本一之后的候选任务

以下内容没有主线编号，必须在 P7 完成后重新评审：

| 候选 | 启动条件 |
| --- | --- |
| 按宿主实现 UI Automation | 具名应用探针证明可稳定读取，且结构化通道不存在 |
| 默认关闭的 OCR | UIA 无法读取并且用户明确允许；图像不落盘 |
| 需求理解和幻觉事件 | 已有人工标注、验证证据和独立评价指标 |
| 受控执行解法 | 解法步骤结构化、人工标记安全、逐次批准和完整审计均已实现 |
| 向量检索 | 确定性加 trigram 在真实语料上无法达到目标，并有离线本地方案 |
| 团队和云同步 | 完成身份、权限、密钥管理和数据删除设计 |

## 15 下一步

下一张任务卡是 `EKB-001`：在新的真实 Git clone 中建立可回滚基线，把本文件和英文版本带入该工作区，然后记录上游 HEAD 和当前失败测试。不要直接从 Go 代码或窗口监控开始。

