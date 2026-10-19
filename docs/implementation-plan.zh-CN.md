# errkb 技术错误复用工具箱实施计划

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
- 后续实现口径：平台核心按 Go 实现；DSH 连接器按锁文件的 `0.1.5-rc.3` 和宿主 0.2.x 实现；不为 rc.2 或 rc.3 任一侧维护兼容分支。README 版本表留到 EKB-055 文档同步时再改。

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
| `README.md` | 重写 | 英文项目入口，描述平台加连接器，不再把旧设计文档作为真源 |
| `README.zh-CN.md` | 重写 | 中文项目入口，与英文逐节同步 |
| `README.toolbox.md` | 删除 | 内容被主 README 和本计划完全覆盖 |
| `README.toolbox.zh-CN.md` | 删除 | 与英文入口一同删除 |
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
| ☑ | EKB-011 | 建立 schema 兼容规则 | 006-010 | 已完成：`spec/compat.cue` 登记五个契约（error_event/config/connector_capability/mcp_tools/local_api）的字段、必填、枚举、未知字段策略、版本锚点和解码器，并定义 R1–R16 规则；新增 `compat` CI job：先自测 16 条规则，再导出 base 与 HEAD 清单并分类差异，破坏性或语义变更必须把 schema_version 从 vN 提升到严格更大的值；实测删除 `command` 字段且不升版本时输出 `violation [R9] ...` 并以 1 退出，同一删除升到 v2 后只记录 `[R3] ... (breaking)` 并以 0 退出；`cue vet ./spec/...` 通过；四份严格解码器对应策略为 reject，ErrorEvent 持久行的宽松读取留给 EKB-053 |

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
| ☐ | EKB-029 | 实现 Named Pipe 本地 API | 008、028 | 默认不开放 TCP；按 Windows 用户限制访问；请求包含 schema 版本 |
| ☐ | EKB-030 | 实现基础 CLI | 024、029 | search、show、record、list、feedback、stats 全部支持人类输出和 `--json` |
| ☐ | EKB-031 | 实现 `errkb run --` | 016、029 | 捕获 stdout、stderr、退出码、命令和环境摘要；重跑同一错误命中同一 issue |
| ☐ | EKB-032 | 实现日志尾随 | 016、029 | 支持 UTF-8、UTF-16、轮转、截断和重复片段抑制 |
| ☐ | EKB-033 | 实现断线脱敏队列 | 014、028 | 数据库断开时只写已脱敏事件；恢复后幂等重放；队列有大小和期限上限 |
| ☐ | EKB-034 | 实现 `errkb doctor` | 028-033 | 显示版本、数据库、迁移、扩展、Pipe、连接器、队列和隐私配置 |
| ☐ | EKB-035 | 完成 CLI 技术错误闭环 | 030-034 | 一条真实失败命令首次建 issue，第二次返回已知解法并记录 retrieval 和 outcome |

P4 是第一个可用 MVP 门槛：不依赖任何特定大模型，用户已经能采集、搜索、记录解法并复用。

### P5 宿主连接器和诊断前回传

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☐ | EKB-036 | 定义连接器 SDK 和能力探针 | 006-008、029 | 统一 capture、lookup、notice、feedback 接口；能力不足时明确降级 |
| ☐ | EKB-037 | 实现 MCP stdio 服务 | 008、030 | lookup、search、record、feedback、list、stats 可由真实客户端调用；说明 MCP 不等于被动捕获 |
| ☐ | EKB-038 | 将 DSH 插件重构为连接器 | 003、036 | `src/plugin.ts` 和 `src/tools.ts` 调核心 API；断线有明确降级；不重复计数 |
| ☐ | EKB-039 | 完成 DSH 未实现项 | 038 | 接通 `agent/request-error` 和 providers；真实 profile 完成捕获和诊断前回传 |
| ☐ | EKB-040 | 实现 Codex 连接器 | 036-037 | 先记录当时版本的能力探针，再用官方 Hook 插件或 MCP 路径完成闭环；禁止依赖 UI 抓取作为主要方案 |
| ☐ | EKB-041 | 实现 Claude Code 连接器 | 036-037 | 先记录 Hook、结构化输出和 MCP 能力，再完成闭环 |
| ☐ | EKB-042 | 建立其他宿主配置模板 | 031-037 | DeepSeek、Kimi、Qianwen 等先提供 CLI 日志或手动模板，不虚假声明专用支持 |
| ☐ | EKB-043 | 建立连接器兼容矩阵 | 038-042 | 每个版本、通道、能捕获字段、能否诊断前回传和降级方式都有实测记录 |

P5 门槛：通用 CLI、DSH、Codex 和 Claude Code 各有一次真实的“错误 -> 查询 -> 解法 -> outcome”录制验收。

### P6 完成 Windows 产品界面

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☐ | EKB-044 | 实现托盘和采集状态 | 028、034 | 运行、暂停、数据库故障和队列积压状态；退出能干净停止服务 |
| ☐ | EKB-045 | 实现浏览器式 omnibox | 024、044 | 输入编号、错误描述、错误码或命令均可查；键盘导航、匹配原因、近似标识和空态 |
| ☐ | EKB-046 | 实现问题详情和解法编辑 | 021、045 | 查看 occurrence、环境、解法版本和 outcome；所有修改写审计 |
| ☐ | EKB-047 | 实现连接器和隐私设置 | 007、043-046 | 每个连接器可启停；采集范围、保留期限和删除操作明确 |
| ☐ | EKB-048 | 实现通知和一键复制 | 024、044-047 | 只返回 top 1 到 3，显示适用环境和可信度；不抢焦点，不自动执行 |
| ☐ | EKB-049 | 实现统计和审计视图 | 021、048 | 展示命中率、解法成功率、注入字符和 token 估算；节省 token 明确标为估计 |

P6 门槛：用户不需要打开数据库或终端，就能暂停采集、搜索、编辑、反馈和审计一次复用过程。

### P7 打包 发布和维护

| 状态 | ID | 任务 | 前置 | 产出和验收 |
| --- | --- | --- | --- | --- |
| ☐ | EKB-050 | 构建 Windows 发布二进制 | 035、049 | 没有 Go 环境的干净机器可以启动 `errkb.exe`；资源和版本信息可重复生成 |
| ☐ | EKB-051 | 实现 PostgreSQL 安装和生命周期 | 004、027、050 | 安装、启动、升级、备份、停止和卸载均有明确数据保留选择；无孤儿服务 |
| ☐ | EKB-052 | 完成干净虚拟机验收 | 050-051 | 首次向导到真实命令错误闭环全部通过；普通用户权限和重启后自启动均验证 |
| ☐ | EKB-053 | 建立升级和兼容测试 | 011、052 | 旧配置、旧 schema、旧 Markdown 和旧连接器升级不丢数据；失败可回滚 |
| ☐ | EKB-054 | 建立发布工作流 | 050-053 | tag、changelog、校验和、SBOM、第三方 notice 和签名策略齐全 |
| ☐ | EKB-055 | 收口中英文文档 | 054 | 两份 README、本计划、安装、卸载、隐私和连接器矩阵逐节同步；删除冗余 toolbox README |

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
| EKB-030 | `cmd/errkb/main.go`（子命令分派）→ `cmd/errkb/cli.go` → `internal/service/client.go` |
| EKB-031 | `internal/capture/run.go` → `internal/capture/run_test.go` |
| EKB-032 | `internal/capture/tail.go` → `internal/capture/tail_test.go` |
| EKB-033 | `internal/service/queue.go` → `internal/service/queue_test.go`；`src/state.ts` 只保留连接器本地队列 |
| EKB-034 | `cmd/errkb/doctor.go` → `internal/service/doctor.go` |
| EKB-035 | `tests/e2e/`（真实命令场景脚本）→ 一次端到端演示（首次建 issue、第二次回已知解法） |
| EKB-036 | `spec/connector.cue` → `internal/capture/connector.go` → 能力探针测试 |
| EKB-037 | `internal/mcp/server.go` → `internal/mcp/tools.go` → `internal/mcp/tools_test.go` → `cmd/errkb/mcp.go` |
| EKB-038 | `src/plugin.ts` → `src/tools.ts` → `src/index.ts` → `tests/plugin.test.ts`（断线与恢复） |
| EKB-039 | `src/index.ts`（接通 `agent/request-error`）→ `src/resolve-detect.ts` → `tests/injection.test.ts` |
| EKB-040 | `connectors/codex/probe.md`（先记录当期能力）→ `connectors/codex/` 连接器文件 |
| EKB-041 | `connectors/claude/probe.md` → `connectors/claude/` 连接器文件 |
| EKB-042 | `connectors/templates/cli-log/config.yaml` → `connectors/templates/cli-log/README.md` |
| EKB-043 | `docs/connector-matrix.md`（中英文同步） |
| EKB-044 | `internal/ui/tray.go` → `internal/ui/status.go` → `cmd/errkb/ui.go` |
| EKB-045 | `internal/ui/omnibox.go` → `internal/service/search_api.go` → 空态与键盘导航测试 |
| EKB-046 | `internal/ui/detail.go` → `internal/service/edit_api.go` → 审计断言 |
| EKB-047 | `internal/ui/settings.go` → 复用 `internal/contract/config.go`（不新造配置模型） |
| EKB-048 | `internal/ui/notify.go` → `internal/service/notice.go` |
| EKB-049 | `internal/audit/log.go` → `internal/audit/stats.go` → `internal/ui/stats.go` |
| EKB-050 | `cmd/errkb/version.go` → `packaging/windows/versioninfo.json` → `packaging/windows/build.ps1`（真实构建） |
| EKB-051 | `packaging/windows/postgres.ps1` → `packaging/windows/installer.iss` → `internal/service/pg_manage.go` |
| EKB-052 | `docs/acceptance-vm.md` → `packaging/windows/vm-acceptance.ps1` |
| EKB-053 | `spec/compat.cue`（扩展）→ `db/migrations/testdata/upgrade/` → `internal/database/upgrade_test.go` |
| EKB-054 | `packaging/windows/release.ps1` → `.github/workflows/release.yml` → `CHANGELOG.md` |
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
