# dsh-errkb · 报错回收再利用

![状态](https://img.shields.io/badge/status-P5%20complete-yellow)
![许可证](https://img.shields.io/github/license/jingchangzhao-gif/dsh-errkb)
![DeepSeek Harness](https://img.shields.io/badge/DeepSeek%20Harness-plugin-blue)

[English](README.md)

> 回收报错：同一个失败只诊断一次，记录下来，下次出现时直接作为已知解法交回给模型。

`dsh-errkb` 是一个 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)（`dsh`）插件。它把模型遇到的报错变成一份带编号、人可编辑的知识库 —— 然后在模型**开始诊断之前**，把已记录的解法塞回上下文。

> **状态：P5 已完成。** 设计文档已写完、正在审查中 —— 其中 §17 的 8 项里还有 6 项
> 未拍板（第 2、4 问已定）—— 工作被拆成 **18 个 task**，归入 7 个里程碑。
> **T01–T15 已完成：** 包装得上、构建得出、过得了类型检查与 lint、格式与测试也都
> 跑得通（99% 覆盖率门槛已真正生效），CI 在每次推送到 `main` 与每次 PR 上
> 都会执行这五步；纯本地层已经齐全：知识库目录解析（T05）、报错规范化与指纹（T06）、强制脱敏（T07）、
> `ERRORS.md` 存储（T08）与匹配（T09）。采集层已经接通：分类与噪声规则（T10），
> 以及前两个钩子 `agent/error` 与 `tools/result`（T11）。注入层已经开口：通知
> 文本、上限与解法信任（T12）已接到四个注入点上（T13），解决检测（T14）把闭环补上。
> 五个工具（T15）让模型能查库、能把解法写下来。
>
> **装上之后，插件会记录，也会注入。** 装进 profile 后，`apply` 会打出知识库位置，
> 把工具失败、命令非零退出和回合级异常记进 `ERRORS.md`，并在同一报错再次出现时、
> 在下文的上限之内把已记录的解法交回给模型。已记录的报错不再失败时，它的解法会获得
> 信任；没有解法的条目会收到一次提示，请模型用 `err_record` 记下解法（T14、T15），
> 由它写进 `ERRORS.md`。还缺：LLM 请求失败的采集（T16），以及安装进 profile（T17）
> —— 目前还没有任何东西替你装上它，所以只有你自己把插件加进 profile 之后，模型才
> 看得到这些工具。下面凡是属于 T16–T18 的部分，依然只是目标路径的描述。
>
> - 设计文档：[`docs/设计说明书.md`](docs/设计说明书.md) —— 19 节
> - 它将来会做什么：[它怎么工作](#它怎么工作)
> - 你的数据将落在哪里：[知识库落在哪里](#知识库落在哪里)
> - 还开放的问题：[待拍板的事项](#待拍板的事项) —— §17 第 2、4 问已定，其余 6 问待定

## 目录

- [目前完成到哪一步](#目前完成到哪一步)
- [为什么需要它](#为什么需要它)
- [能力](#能力)
- [它怎么工作](#它怎么工作)
- [知识库落在哪里](#知识库落在哪里)
- [ERRORS.md 的格式](#errorsmd-的格式)
- [注入：什么时候开口、说多少](#注入什么时候开口说多少)
- [token 账本](#token-账本)
- [环境与兼容性](#环境与兼容性)
- [安装](#安装)
- [用法](#用法)
- [配置](#配置)
- [隐私与脱敏](#隐私与脱敏)
- [失败模式与安全](#失败模式与安全)
- [已知局限](#已知局限)
- [怎么确认它真的在工作](#怎么确认它真的在工作)
- [开发路线图](#开发路线图)
- [开发](#开发)
- [常见问题排查](#常见问题排查)
- [卸载](#卸载)
- [待拍板的事项](#待拍板的事项)
- [后续想法](#后续想法)
- [相关项目](#相关项目)
- [参与与反馈](#参与与反馈)
- [许可证](#许可证)

## 目前完成到哪一步

| 内容                                                                     | 状态           |
| ------------------------------------------------------------------------ | -------------- |
| `docs/设计说明书.md` —— 设计文档（19 节）                                | ✅ 已写完待审查 |
| `README.md`、`README.zh-CN.md`                                           | ✅ 就位         |
| `LICENSE`                                                                | ✅ 就位         |
| `.github/` —— CI、隐私守卫与 PR 模板                                      | ✅ 就位         |
| `package.json`、`tsconfig.json`、`tsdown.config.ts`、`vitest.config.ts`  | ✅ 就位（T01–T03） |
| `.prettierignore`、`.gitattributes` —— 格式与行尾策略                    | ✅ 就位          |
| `cordis.patch.yml` —— bundle patch                                       | ✅ 就位（T04）    |
| `src/index.ts` —— 插件入口（`name`、`inject`、`Config`、`apply`）        | ✅ 就位（T04）；`apply` 读取配置，注册 `agent/error` 与 `tools/result` 监听器（T11）、四个注入点（T13）以及五个工具（T15） |
| `src/plugin.ts` —— 采集流水线与注入接线                                  | ✅ 就位（T11、T13–T15），语句与行覆盖 100% —— T15 加上了 `write()` 与 `err_stats` 读取的通知计数 |
| `src/paths.ts` —— 库目录解析                                             | ✅ 就位（T05），覆盖 100% |
| `src/signature.ts` —— 规范化与指纹                                       | ✅ 就位（T06），覆盖 100% —— 由采集与匹配调用 |
| `src/redact.ts`、`src/redact-patterns.ts` —— 强制脱敏                    | ✅ 就位（T07），覆盖 100% —— 存储每次写入都会套用 |
| `src/store.ts` —— 解析、渲染、追加、归档                                 | ✅ 就位（T08），语句与行覆盖 100% —— 由 T11 的监听器与 T15 的工具写入；为 `err_forget` 新增 `archive(id, reason)` |
| `seeds/ERRORS.seed.md` —— 三条精选、已脱敏的种子条目                      | ✅ 就位（T08）—— 尚未被复制进任何知识库 |
| `src/match.ts` —— 精确、模糊与兜底匹配                                   | ✅ 就位（T09），语句与行覆盖 100% —— 每次写入前先查 |
| `src/state.ts` —— `state.json`、`.machine.json`、环境指纹                | `state.json` 里的命中计数与解法信任计数：✅ 就位（§4.3、T12），语句与行覆盖 100%。`.machine.json` 与环境指纹：⛔ 未开始 |
| `src/capture.ts` —— 分类、标题行提取与噪声规则                           | ✅ 就位（T10），语句与行覆盖 100% —— 由 T11 的监听器调用 |
| `src/inject.ts` —— 通知生成、硬上限与解法信任                            | ✅ 就位（T12），语句与行覆盖 100% —— 已接到四个注入点（T13） |
| `src/resolve-detect.ts` —— 解决检测                                      | ✅ 就位（T14），语句与行覆盖 100% —— 由 `tools/result` 驱动；`recordFix()` 由 `err_record`（T15）调用 |
| `src/tools.ts` —— 五个工具                                               | ✅ 就位（T15），语句与行覆盖 100% —— `err_lookup`、`err_record`、`err_list`、`err_forget`、`err_stats`，经 `ctx.tools.register()` 注册 |
| `tests/`                                                                 | ✅ 554 个用例（非 Windows 上跳过一个）：`paths` 39（T05）、`signature` 30（T06）、`redact` 52（T07）、`store` 62 与 `seeds` 5（T08、T15）、`match` 40（T09）、`capture` 67（T10）、`plugin` 44（T11、T14）、`inject` 70（T12–T15）、`injection` 68（T13、T14）、`resolve-detect` 19（T14）、`tools` 40（T15）、`state` 18（§4.3） |
| 装进 `web` profile                                                       | ⛔ 未开始       |
| 发布到 npm                                                               | ⛔ 未开始 —— 还没有 task 覆盖它，见[开发路线图](#开发路线图) |

## 为什么需要它

模型没有「上次遇到过」的记忆，所以每次都从零重新推导：思考、失败一次、再失败一次。代价是 800 到 3000 token。

只出现一次的失败，这没什么。但每周都撞一次的失败 —— 被占用的 `node_modules` 抛 `EPERM`、`CONTEXT_OVERFLOW`、provider 名字拼错 —— 就是纯浪费。人一眼能认出「这不就是上次那个吗」，模型认不出，因为路径变了、行号变了、PID 也不一样。

`dsh-errkb` 把知识留在磁盘上，需要时把已知的东西注入进去，补上这个缺口。

## 能力

> **设计已定，大部分已实现，尚未安装。** 以下每一条都在设计文档里有规格、有测试计划。
> 「记录」与「注入」都在跑：稳定编号、同一报错一个编号、强制脱敏，对 T11 监听器
> 写入的内容生效；已记录的解法会在诊断之前注入（T13）；报错看起来已解决时会被察觉，
> 并一次性请模型给出解法（T14）；五个工具把回答写下来、并提供账本（T15）。到 T17
> 之前没有任何东西替你把插件装进 profile —— 见
> [目前完成到哪一步](#目前完成到哪一步)。

| 行为                 | 细节                                                     |
| -------------------- | -------------------------------------------------------- |
| **编号稳定**         | 每个不同的报错拿到一个编号，从 `E-0001` 起，永不变更     |
| **同种报错一个编号** | 路径、行号、PID、时间戳变化都仍算同一种                  |
| **诊断前注入**       | 命中时把已记录的解法放进上下文 —— 约 80 token            |
| **人可编辑**         | `ERRORS.md` 就是普通 Markdown；你手写的解法立刻生效      |
| **强制脱敏**         | 密钥、邮箱、绝对路径在落盘之前就被替换掉                 |
| **靠 git 跨设备**    | 追加式块干净合并；计数器留本机，永不冲突                 |
| **可审计的账本**     | `err_stats` 报告命中数、注入量与估算节省的 token         |
| **对压缩友好**       | 事实在磁盘上、按需注入，不躺在对话历史里                 |
| **完全本地**         | 匹配、存储、查询不调用任何模型，也不碰网络               |
| **上限即设计**       | 每一次注入都有硬上限，上限本身就是规格的一部分           |

## 它怎么工作

```
1. 采集         agent/error · tools/result · 非零退出
                （agent/request-error：尚未接通，T16）
                只观察 —— 永不抛错，永不接管重试
                                     │
                                     ▼
2. 指纹         normalize：去 ANSI；ts / pid / port / line / uuid / tmp → 占位符
                绝对路径 → <path>，保留文件名
                signature = sha256(category + "\0" + message)[0:12]
                                     │
                                     ▼
3. 匹配         精确指纹 → Jaccard 模糊（≥ 0.72）→ 短消息
                同 code 兜底 → 都不中则分配新编号
                                     │
                       ┌─────────────┴─────────────┐
                    命中│                           │未命中
                       ▼                           ▼
4. 注入      每条 ≤ 120 token             5. 记录   向 ERRORS.md 追加块
   每步 ≤ 1、每回合 ≤ 3、同编号 ≤ 2                分配下一编号（只增不改）
                       ▲                           ▲
                       └─────────────┬─────────────┘
                                     │
6. 解决        模型或用户通过 `err_record` 写解法（唯一入口）
               解决检测 → 一次性提示 → 状态转 `fixed`
```

难的是第 3 步，不是第 2 步。规范化一条消息是机械的；判定两条看起来不一样的消息**是同一回事**，才是知识库要么帮上忙、要么悄悄用错误解法污染上下文的分水岭。匹配分三层跑 —— 精确指纹、模糊 token 相似度、消息太短时的同 code 兜底 —— 近似命中会在注入文本里被标注为「近似」，而不是当成确定结论蒙混过去。

**有一条规则撑住了整个设计**：`ERRORS.md` 是唯一真源。`errors.index.json` 是由它派生的缓存，可删可重建；`state.json` 存本机计数器，丢了也无所谓。三者永不互写对方的事实。

### 什么才配拿一个编号

不是每个失败都值得占用一个编号。瞬时失败只计数，反复出现才升级 —— 限流不是知识。

| 来源                 | 钩子                              | 记成什么                                                        | 立即编号？                                                                        |
| -------------------- | --------------------------------- | --------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| LLM API 失败         | `agent/request-error`（waterfall）| `code` + 规范化后的 message                                     | 永久性 code：`AUTH`、`QUOTA`、`INVALID_REQUEST`、`CONTEXT_OVERFLOW`、`NO_ADAPTER`、`UNKNOWN` → ✅ 立即 —— **尚未接通（T16）** |
| 回合级异常           | `agent/error`（emit）             | `error.message` / `code` / `name`                               | ✅ 立即（`unknown` 时安全字符串化）                                                |
| 工具失败             | `tools/result`（emit）            | `exec.name` + `result.error.message` + `result.error.info.code` | ✅ 立即（`result.isError`）                                                        |
| 命令非零退出         | `tools/result` 内容嗅探           | 匹配 `\[exit code: (\d+)\]` 且 N ≠ 0                            | ✅ 立即，除非关掉 `captureExitCodes`                                               |
| 瞬时 LLM 失败        | `agent/request-error`             | `RATE_LIMIT`、`SERVER`、`TIMEOUT`、`TRANSPORT`、`EMPTY_RESPONSE` | ❌ 只计数 —— 同一会话内累计到 `transientThreshold`（默认 5）才升级为条目 —— **尚未接通（T16）** |

等它落地（T16），`agent/request-error` 上的监听器必须 `await next()` 并原样返回下游结果。它只观察，永不接管恢复。

**今天接通了什么（T11）。** 监听器的处理体在 `src/plugin.ts`，`apply` 注册其中两个。`agent/error` 把它的 `error` 作为回合级异常交给分类。`tools/result` 把失败结果（`result.isError`）记为工具失败，带上 `result.error.message` 与 `result.error.info?.code`；其余结果则从文本块里嗅探 `[exit code: N]`，命令取自调用参数里的 `command`、`cmd` 或 `script`。每条分好类的报错先与 `ERRORS.md` 里已有的条目匹配：命中则在本机 `state.json` 里给该条目的命中数与最近时间加一，`ERRORS.md` 不动（§4.3）；未命中则追加下一个编号。`count-only` 的报错（未达阈值的瞬时错误，或已升级之后的重复）只以同样方式给已有同指纹的条目加计数，否则什么也不写。瞬时计数按会话（`Agent.id`）分开；没有 agent 的载荷共用一个插件级计数器。`agent/request-error` 还没注册（T16），所以没有任何 LLM 失败会进入流水线。

**只升级一次。** 瞬时错误按会话、按指纹计数，在计数**达到** `transientThreshold` 的那一次拿到编号 —— 之后不会每次都再升级。`capture` 里没列出的来源什么都不产出，连计数都没有。

**签名只看一行（已定，[`docs/discussions.md`](docs/discussions.md) §2a）。** 多行输出 —— 带四十个错误的 `tsc`、一段 `pnpm install` 日志、一个 Python traceback —— 在算指纹前先压成一行标题，所以多出第四十一个错误、或日志顺序变了，编号都不变。`src/capture.ts` 的取法：

1. Python traceback（含 `Traceback (most recent call last):`）取最后一个非空行 —— 这条最先判断，因为 traceback 会引用 `raise ValueError(...)` 这样的源码行；
2. 否则取第一个匹配 `ERR_[A-Z0-9_]+|E[A-Z]{2,}|[A-Z]\w*Error|error TS\d+` 的行（`ERR`、`ERROR` 这类日志级别词不算）；
3. 否则取最后一个非空行。

标题行最长 200 字符；来源没给 code 时，标题行里的 code（`ERR_PNPM_…`、`EPERM`、`TS2307`、`ModuleNotFoundError`）记为条目的 `code`；完整文本留作原始样本。对命令而言，harness 自己追加的 `[exit code: N]` 标记不参与选标题行。命令的标题行不含 code 时——无输出的失败只剩 `exit code 1`，测试工具最后一行是 `1 test failed`——由所执行的命令领起标题行（`pnpm test → exit code 1`，取命令首行，截到 120 字符），这样两条不同命令的无输出失败永远不会共用一个编号。

## 知识库落在哪里

`kbDir` 按三级顺序解析：

| # | 条件                                                                        | 解析结果                                                       |
| - | --------------------------------------------------------------------------- | -------------------------------------------------------------- |
| 1 | 配置了 `kbDir`                                                              | 相对路径 → 基于**插件包根**；绝对路径 → 原样。设备本地差异只写在这里 |
| 2 | `kbDir` 为空，插件包根可写且**不在 `node_modules` 内**                      | `<插件根>/errors/`                                             |
| 3 | 否则 —— npm 全局安装、包目录只读、磁盘不可写                                | `$DSH_HOME/errkb/`                                             |

启动时插件会用一行日志打印**解析后的真实路径**，所以位置永远不是猜的；`err_stats` 也会打印它。

**真实条目放在私有仓库里**（§17 第 4 问）。文本规则脱敏不可能完备，所以本公开仓库整个忽略 `errors/`，只在 [`seeds/ERRORS.seed.md`](seeds/ERRORS.seed.md) 里放精选、已脱敏的种子条目。要跨设备同步你的知识库，就在每台机器上 clone 一个你自己的私有仓库，并让 `kbDir` 指向它 —— 绝对路径，或相对插件根的路径都行。否则 `link:` 开发安装会落在本仓库的 `errors/` 里，而 git 现在会忽略它。无论哪种，**文档里永远不出现绝对路径**。

这个目录里有什么：

| 文件                | 角色                                                                     | 怎么对待它                                              |
| ------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------- |
| `ERRORS.md`         | **唯一真源** —— 编号条目，只追加                                         | 可手改；提交进你的私有知识库仓库                        |
| `ERRORS.archive.md` | 超出 `maxEntries` 后归档的老条目，只追加                                 | 提交进同一个私有仓库                                    |
| `errors.index.json` | 派生缓存：指纹 → 编号                                                    | 可删，会从 `ERRORS.md` 重建；建议 gitignore             |
| `state.json`        | 本机状态：各条目在本机的命中数与 `lastSeen`，以及解法信任计数（环境指纹尚未实现） | 可删；**本机文件，绝不提交**                            |
| `.machine.json`     | 设备标识 `deviceSlug`（如 `DESKTOP-A`），用于条目里的设备字段            | 可删可重建；设备本地文件，同样别进 git                  |
| `.lock`             | 瞬时写锁，写完即删                                                       | 忽略它                                                  |

给那个私有知识库仓库一份够用的 `.gitignore`：

```gitignore
state.json
errors.index.json
.machine.json
.lock
```

设计文档明确点名了 `state.json` 与 `errors.index.json`；`.machine.json` 与 `.lock` 出于同样的理由（设备本地 / 瞬时）也不该进版本库。

这个分工就是跨设备的全部故事：知识靠往 `ERRORS.md` 追加来合并，计数器留在产生它的那台机器上，两者永不打架。至于哪些还没建，见[开发路线图](#开发路线图)。

## ERRORS.md 的格式

编号从 `E-0001` 起，由 `idPrefix`（默认 `E-`）与 `idWidth`（默认 `4`）拼出，并且**只增不改 —— 编号一旦写下就永不变更**，连合并冲突也不例外。

单条目样例（默认的英文字段名）。机器字段装在 HTML 注释里，渲染后干净，手工编辑也安全：

````md
## E-0007 · [tool:pwsh] EPERM: operation not permitted, rename
<!-- errkb: sig=3f2a1c9d0b71 cat=tool code=EPERM first=2026-09-14T09:12:33Z -->

- Fingerprint: `3f2a1c9d0b71`
- Category: `tool / pwsh`
- First seen: 2026-09-14 09:12 · Last seen: 2026-09-14 15:40 · Hits: 5
- Trigger: `pnpm install` writing `node_modules` under a non-ASCII path while another process holds it
- Raw message:
  ```text
  EPERM: operation not permitted, rename '<path>\node_modules\.pnpm\<hash>'
  ```
- Fix:
  Close the editor or real-time antivirus scan holding the directory, then re-run `pnpm install`; if it persists, use `pnpm install --config.node-linker=hoisted`.
- Status: `fixed`
- Notes:
````

**字段名**：默认英文。设 `labels: 'zh'` 则改写设计文档 §8 的中文字段名（`指纹`、`分类`、`首次`、`最近`、`命中`、`触发`、`原始信息`、`解法`、`状态`、`备注`）。不管怎么设，解析器两套都认（全角 `：` 也认），所以一份文档可以混用；更新某条时会沿用它原来的语言。注释里的机器 key 永远是英文。

**命中与最近时间**：块里写的值是建条目时的值（`命中: 1`、采到的时间），或此后有人手工改成的值。重复出现从不重写这个块：它按编号计在本机的 `state.json` 里，所以两台设备都命中同一个报错时不会碰同一行，git 历史可以干净合并（§4.3）。插件显示的每个数字 —— 通知、开场摘要、`err_lookup`、`err_list`、`err_stats` —— 都是块里的命中数加上本机的增量，最近时间取两者较晚的一个；所以各设备上的数字可以不同，知识保持一致。删掉 `state.json`，计数就回到块里写的值。改解法、状态或备注（手工或经 `err_record`）仍会重写那一个块。

**解析规则**：以 `^## (E-\d+) ·` 切块；块内 `<!-- errkb: ... -->` 提供机器字段；`- Fix:`（或 `- 解法:`）取到下一个已知字段开头的行为止，所以解法里可以有空行、列表和代码。**你对文档的手工修改优先于索引** —— 索引只是缓存，所以你在任意 Markdown 阅读器里补写的解法，下一次命中就能用上。每个块都保留原文：文档读出再写回逐字节一致，更新只重写被改的那一条。

**该严的地方严**：git 冲突标记、格式错误的条目标题、缺机器注释、重复编号、未知状态、未闭合的代码围栏，都会让文档判为无法解析。此时原文件另存一次为 `ERRORS.corrupt-<时间戳>.md`，新条目只追加，更新一律拒绝，直到修好为止。

**条目状态**：`open`、`fixed`、`wontfix`。把条目设为 `wontfix`（或标记为误判）会让它永久退出自动注入，但仍然计数 —— 这样一条坏记录不会反复污染上下文。

> **字段语言已拍板（§17 第 2 问）：** 默认英文字段名，`labels: 'zh'` 切中文，两种都解析。设计文档样例里的 `device` 与 `proj` 机器字段这里没有写：它们随采集层一起到来，而公开模式下要哈希还是丢弃仍未定（`docs/discussions.md` §5）。

## 注入：什么时候开口、说多少

| 情形                                 | 注入点                                                               | 到达模型的内容                                                          |
| ------------------------------------ | -------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| 本步里有工具/命令失败                | `tools/post-execute` → `additionalContexts`                          | 命中：已知解法。`inject: 'always'` 下的未命中：`recorded as E-0011 (no fix yet).`，在下一步送达（见下文） |
| 回合已经死了（以 `agent/error` 报出的 LLM 或回合失败） | 下一次 `agent/pre-step` → `{kind:'enter', messages}` | 与失败那一步匹配的条目通知，追加在这一步自己的入场消息之后              |
| 会话开场                             | `agent/session-start` → `agent.inject()`                             | 由 `sessionDigest` 决定：`off`、`counts`（一行）或 `index`（最多 10 条标题） |
| 常驻                                 | `ctx.systemPrompt.section({name:'plugin:errkb', order:10400})`       | 约 50 token 的固定行为约定：重新诊断之前先试已知解法，新解法用一句话说清 |

命中时模型收到的不是一份报告，而是一条指令，封顶 120 token。插件自己的界面文字是英文，所以模型读到的就是这一行英文：

```
[errkb] E-0007 known (5 hits) | cause: node_modules locked by an editor during
pnpm install | fix: close the locking process and re-run; if it persists, use
pnpm install --config.node-linker=hoisted. Known fix: try this first, before
re-diagnosing or researching.
```

（这里为阅读折了行，真实通知只有一行。）结尾一句是在排顺序（「先试这个，再重新诊断或去查资料」），而不是禁止什么，所以同一步里另一个插件的「去查资料」提示不会和它打架（[`docs/discussions.md`](docs/discussions.md) §1.2）。其余措辞与 `src/inject.ts`（T12）写出的完全一致：

| 情形                         | 通知                                                                                   |
| ---------------------------- | -------------------------------------------------------------------------------------- |
| 近似命中（模糊或 code 兜底） | `… \| fix: … Approximate match, verify first.`                                        |
| 注入后同一报错又出现         | `… \| fix: … This fix failed here last time; verify before applying.`                 |
| 命中但条目没有解法           | `[errkb] E-0007 seen before (5 hits), no fix recorded yet.` —— 很短，约 15 token        |
| 未命中，且 `inject: 'always'` | `[errkb] recorded as E-0011 (no fix yet).` —— 默认的 `hit-only` 下不出声               |
| 没有解法的条目看起来已解决   | `[errkb] E-0011 looks resolved. Record the fix with err_record in one sentence so it can be reused.` —— 每个条目每会话一次，随下一步送达（T14） |
| `wontfix` 或误判条目         | 永不出声                                                                               |

**上限是硬上限，而且上限本身就是设计。** 每步最多 1 条通知、每回合最多 3 条、同一编号每会话最多 2 次；每条通知按保守估算 ≤ 120 token（每个非 ASCII 字符算 1 token，ASCII 每 3 个字符算 1 token），且 ≤ 400 字符。超长时先缩成因、再缩解法，结尾那句指令永远不被截掉。插件来源消息的 `summary` 交给 dsh-llm 自带的 `boundContextSummary`，因此 ≤ 120 字符，对应 `{kind:'plugin', plugin:'err-kb', form:'notice', summary}` 的形状（`@deepseek-ai/dsh-llm` 里的 `MessageSourceMap['plugin']`）。状态为 `fixed` 的条目提示一次后即静默。没有上限的版本比不装还糟：用持续噪声换来的命中率，会把收益变成成本。

**不管用的解法不再被推送**（[`docs/discussions.md`](docs/discussions.md) §4）。注入了某条目的解法之后，同一回合里又采到这一条，就算一次复发：复发 1 次，通知改成「This fix failed here last time; verify before applying.」；复发 2 次且从未成功，本机不再自动注入该条目。改写条目的解法后重新计数。这些计数只属于本机，从不写进 `ERRORS.md`：它们存在 `state.json` 的 `trust` 下，所以被压制的解法重启后依然被压制，且只在本机。删掉 `state.json` 即重新计数。

每个上限都能往小调；`inject: 'off'` 关掉所有通知，而采集照常记录。会话开场摘要与常驻段各有自己的开关：`sessionDigest: 'off'` 与 `systemPromptHint: false`。

**四个注入点是怎么接的（T13）。** 钩子类型都照已安装的包读出：`tools/post-execute`（`PostToolDecision.additionalContexts`，`@deepseek-ai/dsh-tools`）、`agent/pre-step`（`PreStepDecision`，`{kind:'enter', messages}`）、`agent/session-start` 与 `Agent.inject()`（`@deepseek-ai/dsh-agent`）、`SystemPrompt.section()`（`PromptSection`，`@deepseek-ai/dsh-system-prompt`）。两个 waterfall 都先 `await next()`，再把下游结果追加一条消息后返回；没有可加的内容、或者我们这边任何一处出错时，原样返回下游那个对象。

- **热路径只读，写入留在回合之外。** 工具失败在 `tools/post-execute` 里就地分类、对缓存索引做匹配；写入照旧随后由 `tools/result` 完成。读取排在已入队的写入之后，所以上一步刚记下的报错这一步就能命中。
- **未命中的编号，只在它真正存在之后才说。** 钩子内新编号尚未分配，所以 `inject: 'always'` 下的未命中通知既不猜编号、也不说「待定」：它等写入给出编号后，随下一次 `agent/pre-step` 送达；写入失败就什么也不说。
- **死掉的回合，通知随下一步送达。** 回合级错误在采集时就查库，结果追加进下一步的入场消息。LLM 请求失败只有在以 `agent/error` 结束回合时才走这条路；`agent/request-error` 本身属于 T16。
- **按会话计预算。** 每个会话（`Agent.id`）一个上限计数器，只保留最近活跃的 64 个会话。`agent/pre-step` 上出现新的 `turn` 编号即开始新回合，每次 pre-step 开始新一步；`agent/session-start`（含 `clear` 与 `compact`）让该会话的预算从头算。不属于任何 agent 的工具调用永不注入。
- **开场摘要**在 `counts` 下就一行：`[errkb] 37 known errors; known fixes are shown when an error repeats.`；`index` 再列出命中最多的至多 10 条，`wontfix` 与误判条目不列。知识库为空时不发摘要。
- **解决检测（T14，`src/resolve-detect.ts`）。** 会话里一次工具调用被记到某个条目上之后 —— 新条目或命中，注入与否都算 —— 这个条目就按一个键被盯住：命令非零退出用命令行，工具失败用工具名。之后同一会话里、在**窗口**之内 —— 那个回合剩下的部分加上整个下一回合 —— 同一个键上出现一次成功的调用，就算它已解决。条目再次出现时，从这次重现起重新盯，键换成最后失败的那个；所以只跟在较早一次出现之后的成功不算解决。新的会话生命周期（`clear`、`compact`）会忘掉所有在盯的条目。已解决条目的解法在解法信任里记一次成功，这会解除压制。没有解法的条目，在 `captureFix: 'prompt-once'`（默认）且 `inject` 不是 `off` 时，会在下一步收到上表那条一次性提示：它和其他通知一样占用每步与每回合的额度，但有自己的每编号额度；被上限挡住时等后面的步，窗口关闭后就不说了直接丢弃。同一会话里绝不问第二次，`wontfix` 与误判条目从不被问。模型的回答不会从它的自由文本里解析：写入解法的唯一入口是 `err_record`（T15），它调用记录器的 `recordFix(id, fix)`，把解法（由存储层脱敏）写入并把状态设为 `fixed`，不在回合里进行，与其他写入排队。回合级异常（`agent/error`）之后没有工具可以成功，因此不被盯。
- **常驻段**原文如下：`Errors are tracked by the errkb plugin. A context line starting with [errkb] names a known error and, when one is recorded, its fix: try that fix before re-diagnosing. When you resolve an error that has no recorded fix, record a working fix with err_record.` 共 46 个英文单词；T15 把最后一句改成了指向该工具。

## token 账本

划不划算应该由你自己判断，所以把账算在这里。

| 项目                             | 成本 / 收益                                                      |
| -------------------------------- | ---------------------------------------------------------------- |
| 一次命中注入                     | 约 80 token，替换掉通常 800–3000 token 的重新诊断                |
| 常驻 system prompt 段            | 每请求约 50 token（`systemPromptHint: false` 可关）              |
| 会话开场摘要                     | 每会话约 40 token（`sessionDigest: 'counts'`）                   |
| 匹配、指纹、存储、`err_stats`    | 零 —— 完全本地，不调模型、不联网                                 |
| **盈亏平衡点**                   | **命中率大约到 5% 及以上才回本**                                 |

低于这个命中率，固定开销就大于节省，诚实的答案是关掉 `systemPromptHint`，或者干脆别装。`err_stats` 在本地报告命中数、注入量、估算节省的 token 与解析出的库路径 —— 让这个决定建立在数字上，而不是感觉上。

## 环境与兼容性

| 项目          | 值                                                                                    |
| ------------- | ------------------------------------------------------------------------------------- |
| dsh           | `0.1.5-rc.2`（本设计核验所依据的版本）                                                |
| Cordis        | `4.0.2`                                                                               |
| `dsh-tools`   | `0.1.5-rc.2`                                                                          |
| `dsh-llm`     | `0.1.5-rc.2`                                                                          |
| schemastery   | `3.18.2`                                                                              |
| 构建工具      | `tsdown 0.22.2`、`vitest`、`typescript`、`oxlint`、`prettier`                          |
| Node / pnpm   | Node `>=22.13`（`engines.node`）、pnpm `11.7.0`（`packageManager`），均写在 `package.json` |
| 平台          | 在 Windows 上开发与核验。其他平台未测试，也还没有任何 OS 支持范围的声明                 |

下面这些扩展点是**对着本机安装实际核验过的**，不是照着公开文档推测的 —— 文件与行号引用见设计文档 §2：

`agent/request-error` · `agent/error` · `tools/result` · `tools/post-execute` · `agent/pre-step` · `agent/session-start` · `agent.inject()` · `ctx.systemPrompt.section()` · `defineTool` + `ctx.tools.register()` · `{kind:'plugin', plugin, form:'notice', summary}` 消息形状。

`dsh` 目前是 rc 版本。在区间固定下来之前，请把每一次 `dsh` 升级都当成可能的破坏性变更。

## 安装

> **什么都没发布，插件也还没装进任何地方。** 前两条命令从 T01–T04 起可用 ——
> `pnpm install` 与 `pnpm build` 都退出 0 并产出 `lib/index.js` —— 但把包加进
> profile 要到 T17 才验证。加进去之后，它会按[目前完成到哪一步](#目前完成到哪一步)所述记录与注入。

```sh
cd <repo-root>
pnpm install
pnpm build
dsh plugin --profile web add .        # 相对路径，基于当前目录锚定
```

然后**重启** `dsh web`。`dsh.profile.bundles` 的变化不在 `patchReload: live` 的监视范围内，所以是必须重启，不是可选。

声明了 `dsh.bundle.patch` 的包会被自动并入 `dsh.profile.bundles` —— 不需要手工改 profile 配置。又因为开发期是 `link:` 安装，未配置时知识库会落在本仓库的 `errors/` 里，而 git 会忽略它；要跨设备同步，请把 `kbDir` 指向你自己的私有仓库。详见[知识库落在哪里](#知识库落在哪里)。

## 用法

> **已实现（T15），尚未安装。** 五个工具都已存在，由 `apply` 注册；但到 T17 之前
> 没有任何东西把插件装进 profile，所以只有你自己装上之后，模型才看得到它们。

问 agent 一个报错，或者直接调工具：

```json
{
  "query": "EPERM: operation not permitted, rename",
  "full": false
}
```

[注入](#注入什么时候开口说多少)里那一段就是整个产品。它替换掉的是一次本来要花 800–3000 token 的诊断。

### 参数

| 工具         | 参数                                                              | 默认 / 说明                                                  | 返回                                                        |
| ------------ | ----------------------------------------------------------------- | ------------------------------------------------------------ | ----------------------------------------------------------- |
| `err_lookup` | `query`（原文、指纹或编号）、`full?`                              | `full: false` 时不返回原始信息，省 token；依次按编号（`E-7` 能找到 `E-0007`）、指纹、原文（跨所有分类匹配）查找 | 命中的条目（编号、分类、命中数、状态、解法）；未命中则 `null` + 按 Jaccard 最接近的 3 条 |
| `err_record` | `id?`、`message?`、`fix?`、`status?`、`note?`、`category?`        | `id` 与 `message` 恰好给一个；`status` 取 `fixed`/`wontfix`/`open`；只给解法时状态转 `fixed`；备注作为新的一行追加 | 编号，以及是否新建                                          |
| `err_list`   | `cat?`、`status?`、`limit?`                                       | `limit` 默认 20，最多 200；`cat` 可以是采集分类（`tool`）或显示分类（`tool / bash`） | 编号、标题、命中数 —— 不含正文                              |
| `err_forget` | `id`、`reason?`                                                   | 移入 `ERRORS.archive.md`，备注里写上 `Archived <日期>: <原因>`；不真删，编号也永不复用 | 条目是否已归档                                              |
| `err_stats`  | `scope?`                                                          | `scope` 取 `session` 或 `all`（默认）；它只决定通知计数的范围，条目数据始终是整个库 | 条数、命中数、已注入通知、估算节省的 token、没有解法的 open 条目、被压制的解法、解析出的库路径 |

五个工具的输出统一走 `output.schema` + `render`（`@deepseek-ai/dsh-tools` 的 `ToolOutputDefinition`），与 dsh-note 同款，保证模型侧收到的是受控纯文本；它们用 `defineTool` 定义，经 `ctx.tools.register()` 注册。

- **`err_record` 是写入解法的唯一入口。** 给 `id` 时，解法走记录器的 `recordFix()`（条目转为 `fixed`），然后再用一次写入改状态与备注，所以显式给出的 `status` 优先。给 `message` 时，按采集时的方式匹配（取标题行，在 `category` 或所有分类里找）；只有精确命中才更新那个条目（不计命中数）；近似命中 —— 模糊或按错误码 —— 什么都不写，返回 `closest match is E-0007 (approximate, by fuzzy); nothing was written. Call err_record with id: "E-0007" to confirm, or reword message`，免得解法落到一个相似却不同的条目上；未命中则以 `category`（默认 `agent`）追加新条目。匹配与追加在同一次写入里完成，所以关于同一个新报错的两次调用只会建一个条目。
- **所有写入都走采集的写入路径。** 它们都在知识库唯一的写入链上排队，同样是 500 ms 预算与重试；存储层对每段文本脱敏。锁一直被占或写入失败，都以错误结果返回，绝不抛出。
- **错误是返回值。** 参数类型不对、`status`/`scope` 取值未知，由 `defineTool` 的校验拒绝（`ToolArgsError`）；其余情况 —— `id` 与 `message` 同时给或都不给、编号不存在、解法为空、`ERRORS.md` 读不出来 —— 返回一行 `error`，例如 `err_record: no entry E-0042`。
- **token 估算明确标为估算。** `err_stats` 的算法是：*带解法的通知数 × 800 − 所有已送达通知的 token 数*。800 取的是 §7 里一次重新诊断 800–3000 token 的下限；常驻开销（每次请求的系统提示段、会话摘要）没有扣除。这个数字不会低于 0：通知花掉的 token 比假定省下的还多时 —— 送达了通知、却没有一条带解法 —— 显示 0，同一行写出负数的算式，例如 `0 fix notices × 800 − 60 notice tokens = −60, shown as 0`。

## 配置

### 这些配置写在哪

配置写在 profile patch 里，不是另开一个配置文件：

```yaml
# cordis.patch.yml
- insert:
    - id: err-kb
      name: dsh-errkb
      config:
        fuzzyThreshold: 0.72
        inject: hit-only
```

### 配置项

| 配置项               | 默认                               | 用途                                                                                                     |
| -------------------- | ---------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `kbDir`              | `''`                               | 空则自动解析：配置 → 插件根 `errors/` → `$DSH_HOME/errkb/`（[详见](#知识库落在哪里)）。相对路径基于插件包根 |
| `idPrefix`           | `'E-'`                             | 编号前缀（设计文档 §8 定义；§10 的配置表里没有它）。只能含字母、数字、`-` 和 `_`，且不以数字结尾；否则使用 `E-`                                                        |
| `idWidth`            | `4`                                | 编号的补零位宽，1 到 9 的整数                                                                                           |
| `capture`            | `['tool','command','llm','agent']` | 采集开关，逐个可关                                                                                       |
| `captureExitCodes`   | `true`                             | 是否记录非零退出的命令                                                                                   |
| `transientThreshold` | `5`                                | 瞬时 LLM 错误升级为编号条目所需的次数；向上取整，至少为 1                                                                    |
| `fuzzyThreshold`     | `0.72`                             | 模糊匹配所需的相似度，取值 0.5 到 1.0                                                                 |
| `captureFix`         | `'prompt-once'`                    | `prompt-once` 或 `off`：没有解法的条目看起来已解决时，是否每会话一次提示模型说出解法；其他值退回 `prompt-once` |
| `inject`             | `'hit-only'`                       | `hit-only`、`always` 或 `off`；其他取值一律按 `hit-only` 处理                                             |
| `sessionDigest`      | `'counts'`                         | 会话开场摘要：`off`、`counts` 或 `index`（最多 10 条）；其他取值一律按 `counts` 处理                       |
| `systemPromptHint`   | `true`                             | 是否注入那 50 token 的行为约定段                                                                         |
| `providers`          | `['*']`                            | 限定只记录哪些 provider（可填 `deepseek-official`）—— 尚未读取；用到它的 `agent/request-error` 监听器在 T16 接入                                                       |
| `share`              | `'public'`                         | 脱敏强度 —— `public` 或 `private`（[详见](#隐私与脱敏)）                                                   |
| `maxEntries`         | `200`                              | 超出后归档到 `ERRORS.archive.md`；整数，至少为 1                                                                       |
| `maxSampleChars`     | `500`                              | 原始样本的存储上限；整数，至少为 0，`0` 表示不封顶                                                                                       |
| `exportDir`          | `''`                               | 可选的设备本地导出目录；空则关闭（例如某台机器上的 Obsidian 路径）—— 尚未读取（T18）                                        |
| `labels`             | `'en'`                             | 新写入条目的字段名语言：`en` 或 `zh`。两种都始终能解析（[详见](#errorsmd-的格式)）                         |

数值配置超出取值范围、或 `idPrefix` 形状不对时，插件照常启动：改用最接近的合法值（整数型配置的小数部分向下取整，`transientThreshold` 例外、向上取整；不是数字的值和不合规的 `idPrefix` 用默认值），并记一条警告，写明配置项、给定值和实际采用的值。

## 隐私与脱敏

这个插件做的每件事都是本地的：不联网、无遥测，也不会让任何模型去做分类、匹配或摘要。离开这台机器的，只有你自己提交上去的东西。

脱敏是**强制的，而且发生在落盘之前，不是分享之前** —— 未脱敏的原文根本不会到磁盘，只存在于内存里，活到算出指纹为止。落盘前会被替换掉的内容：

- 凭据与请求头：`sk-*`、`Bearer *`、`api[_-]?key=*`、`token=*`（以及 `password=`、`secret=`）、`authorization:`；
- 平台与服务商密钥：GitHub（`ghp_`、`gho_`……、`github_pat_`）、AWS（`AKIA…`）、xAI（`xai-`）、Google（`AIza…`）；
- 长 base64 串、32 位以上十六进制串、邮箱地址；
- 原始 `requestId`；
- 家目录里的用户名（`/home/<名字>`、`/Users/<名字>`、`<盘符>:\Users\<名字>` 变成 `~`）；
- 当 `share: 'public'`（默认）时额外：绝对路径压成 `<path>`，原始样本封顶 `maxSampleChars`（500）字符。

`share: 'private'` 会保留项目内相对路径和其余绝对路径，自查更方便，但前提是这份文件保持私有。

凭据规则只有一份，在 `src/redact-patterns.ts`。CI 的隐私守卫是一条 shell `grep`，表达式是它自己的一份拷贝；有一条测试会解析 `.github/workflows/privacy-guard.yml`，只要那里有一类规则在该文件里找不到对应项就失败，两边不会再悄悄漂移。文本规则脱敏依然不可能完备 —— 内网主机名、SSH 报错里的 `user@host`、长度不够阈值的短 token 都会漏过去 —— 这正是下一段要把真实条目挡在公开仓库之外的原因。

**真实条目永远不进公开仓库（§17 第 4 问）。** 脱敏是兜底，不是方案：知识库应当放在 `kbDir` 指向的私有仓库里，跨设备同步也靠它。本仓库忽略 `errors/`，只公开 `seeds/` 里的精选条目；有一条测试会对每个 `seeds/*.md` 跑 `redact()`，只要会改动一个字符就失败。用私有仓库时，`share: 'private'` 是合理的选择。

目前还没有 `SECURITY.md`，也没有安全策略 —— 在有之前，任何未脱敏原文落进文档的情况都请当 bug 报出来。见[待拍板的事项](#待拍板的事项)。

## 失败模式与安全

插件自己的失误，绝不能变成 agent 的问题。下面每一条风险，在设计文档 §13 里都有一条对应的实现约束。

| 风险                                       | 约束                                                                                                     |
| ------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| 监听器抛错 → 回合直接被关闭                 | 所有监听器包 `try/catch`；失败降级为「本次不记录」，在内存里累计并节流上报                        |
| 抢走 `dsh-llm-retry` 的恢复权               | `agent/request-error` 必须 `await next()` 并原样返回 —— 只观察，不接管                                     |
| 写文件阻塞回合                              | 本地写 + 3 次重试 + 500 ms 超时；超时则跳过该次记录，永不抛错                                              |
| 多进程同写（web + headless 并发）           | `.lock` 文件（`wx` 打开，10 s 过期可抢占；抢占者按 token 只删自己判定过期的那把锁）+ 临时文件 + `rename` 原子替换                                   |
| 磁盘只读、权限失败                          | 自动回退 `$DSH_HOME/errkb/`；再失败则本次静默跳过                                                          |
| 文档被写坏                                  | 严格解析；解析失败时原文件另存为 `ERRORS.corrupt-<时间戳>.md`，新条目只做追加 —— **损坏的文档永不回写**     |
| 密钥/隐私泄进公开仓库                       | 落盘前强制脱敏；`share: 'public'` 时连 `requestId` 都不保留                                                |
| 条目无限膨胀拖慢上下文                      | `maxEntries` 归档 + 每条 400 字符封顶 + 注入去重与每步/每回合/每会话上限                                   |

**今天实际跑了多少（T11）。** 监听器按上表包裹：失败只计数、经插件的 logger 每分钟最多报一次，永不抛出。失败计数只在进程存活期间保存在内存里，不写进 `state.json`；写进去的只有命中计数与解法信任计数。`state.json` 不是合法 JSON 或版本不是 1 时绝不致命：读作空（计数回到块里的值）、只报告一次，并在下一次写入时另存为 `state.corrupt-<时间戳>.json`。写 `state.json` 与写 `ERRORS.md` 用同一把 `.lock`、原子替换文件，并共用这次写入的 500 ms 预算。写入不在回合里进行，同一个知识库一次只写一条，每条有 500 ms 预算，涵盖至多 3 次重试与等锁；预算用完锁仍被占，则静默跳过这次记录。已经拿到锁的写入不会被中途打断，因为存储无法安全地放弃写到一半的文件。解析不了的文档不重试，修好之前不再记录。脱敏、锁、原子写与损坏文档这几行由存储层（T07–T08）负责，对每次写入都生效。插件发起的 promise 没有一个会缺少处理器：`tools/post-execute` 在 `await next()` 之前发起的查找一创建就挂上了兜底，所以畸形的工具结果（比如 `isError` 却没有 `error`）只会被计数、下游决定原样通过，而不会变成可能拖垮宿主进程的未处理拒绝。

**它永远不会做的事**：接管重试、把异常抛进回合、回写损坏的文档、让一次写入阻塞回合、把任何东西发出这台机器。

## 已知局限

**它不接管重试，这是刻意的。** `dsh-llm-retry` 拥有重试所有权，且每次重试都会再次计费。`agent/request-error` 的监听器必须 `await next()` 并原样返回结果。一个在这里「热心地」做恢复的插件，等于在和重试拥有者的正确性契约打架。

**模糊匹配可能把两个只是长得像的报错并成一个。** 0.72 的相似度下，近似命中终究是没命中。缓解手段是社会性的而非算法性的：近似命中会在注入文本里标注为「近似」，且任何条目都能被标为误判，永久退出注入。写入也有把关：带 `message` 调用 `err_record` 只在精确匹配时才更新条目；近似匹配时什么都不写，只返回最接近的编号，模型须带上这个 `id` 再调用 `err_record` 确认。

**0.72 只是占位值，不是实测值。** T09 的测试证明阈值在 0.71 / 0.72 / 0.73 上按代码行为、中文按二元组切分；它们并不证明 0.72 就是对的数。这需要 [`docs/discussions.md`](docs/discussions.md) §3 提议的、由真实报错与「长得像但不同」配对组成的标注语料，而这份语料目前还没有。

**知识库的质量等于写进去的东西的质量。** 一条没有解法的条目，注入时也没有解法 —— 那就是花 token 说了一句废话。这正是新条目会一次性提示补写解法、以及 `err_stats` 会报告有多少 open 条目在空耗的原因。

**它可能变成净亏损。** 命中率低于大约 5% 时，固定开销就超过节省；上限的存在只是让亏损小一点，不是让它消失。见 [token 账本](#token-账本)。

**知识只靠 git 传播，这是唯一通道。** 没有云服务，没有远程共享。两台设备靠合并追加式 Markdown 收敛，可靠，但要手动。

**所有项目共用一个库。** 没有按项目隔离；设计文档 §8 里的项目与设备字段目前也还没有记录 —— 没有任何代码写入它们，所以匹配器「同项目优先」的平局规则实际上不会生效。等这些字段开始记录后，public 模式是对它们做哈希还是直接丢弃，仍是待定问题（[`docs/discussions.md`](docs/discussions.md) §5.3）。

**没有 GUI 面板。** 目前否掉了。设计文档把它记为发布后的候选项。

## 怎么确认它真的在工作

### 验收标准

设计文档 §1 用 7 条标准定义「做成了」，每条都配了验证方式：

| #  | 标准                                                              | 怎么验证                                                              | 当前状态 |
| -- | ----------------------------------------------------------------- | --------------------------------------------------------------------- | -------- |
| S1 | 报错自动入库并获得唯一编号（`E-0001` 起）                          | 制造一次失败工具调用 → `ERRORS.md` 出现新编号块                        | 已由模拟宿主下的单元与集成测试覆盖；真实宿主待 T17 |
| S2 | 同种报错**不重复编号** —— 路径、行号、PID、时间戳变化也算同种       | 换临时目录、换行号重跑 → 命中计数 +1，编号不变                         | 已由模拟宿主下的单元与集成测试覆盖；真实宿主待 T17 |
| S3 | 命中时向模型注入 ≤120 token 的已知解法                            | 会话里出现 `plugin` 来源的 notice 行，模型不再重新诊断                 | 已由模拟宿主下的单元与集成测试覆盖；真实宿主待 T17 |
| S4 | 新报错在解决后被写入「解法」字段                                  | 模型调用 `err_record`，或收到一次性提示后补写                          | 已由模拟宿主下的单元与集成测试覆盖；真实宿主待 T17 |
| S5 | 插件自身的任何失败都**不会**中断回合或影响重试                     | 单测注入异常 → 监听器吞掉，`agent/request-error` 仍返回下游结果        | 已有监听器的吞异常行为有测试；`agent/request-error` 部分待 T16 |
| S6 | 文档可被人和模型双向编辑，机器索引可重建                          | 手工改 `ERRORS.md` 的解法 → 命中时读到改后文本                         | 已由模拟宿主下的单元与集成测试覆盖；真实宿主待 T17 |
| S7 | 跨设备安全：无绝对路径、无密钥                                    | 全文检索 `D:\`、`sk-`、`Bearer` 均为 0 命中                            | 已由脱敏测试与 CI 隐私守卫覆盖；真实宿主待 T17 |

### 手工验收

这些步骤需要把插件装进真实的 `dsh`，所以要等 T17（安装）之后才能执行。

1. `dsh --profile web --dump-config` 能看到 `err-kb` 条目（证明 bundle 层生效）。
2. 触发一次必然失败的命令（例如访问不存在的盘符）→ 出现 `## E-0001`。
3. 换临时目录或行号重跑同类失败 → 仍是 `E-0001`，命中数 +1。
4. 会话里出现 plugin notice 行，模型**不**再重新诊断。
5. `err_stats` 打印解析出的库路径、命中率与估算节省。

## 开发路线图

设计已定稿，工作被拆成 **18 个 task**，归入 7 个里程碑。每个 task 的产出与验收标准见下方[完整任务分解](#完整任务分解)。

| 状态 | Tasks                                                                |
| ---- | -------------------------------------------------------------------- |
| ✅   | T01–T09 —— 工程骨架（package、tsconfig、tsdown、vitest、bundle patch）、paths、signature、redact、store 与 match |
| ✅   | T10 —— 分类、标题行提取与瞬时噪声规则                                |
| ✅   | T11 —— 前两个钩子：`agent/error` 与 `tools/result`                   |
| ✅   | T12 —— 通知文本、硬上限与解法信任                                    |
| ✅   | T13 —— 四个注入点                                                    |
| ✅   | T14 —— 解决检测，注入层的最后一块                                    |
| ✅   | T15 —— 五个工具                                                      |
| 🔜   | T16–T17 —— LLM 失败接入，以及安装进 web profile                      |
| 🔜   | T18 —— 可选：Obsidian 导出                                           |

T01–T15 已勾选，并已全部合并到上游（T14 与 T15 随 PR #12 合并）。其余仍开放；T18 随时可以砍掉，不影响主线。

### 阶段对照

设计文档按 P0–P7 阶段规划（§15），任务清单把同一批工作编成 T01–T18。两者这样对齐：

| 阶段         | Tasks   | 完成标志                                                              |
| ------------ | ------- | --------------------------------------------------------------------- |
| P0           | ——      | 设计文档已写完，且**你已补齐 §17** —— 8 问已答 2 问（第 2、4 问）   |
| P1           | T01–T04 | ✅ `pnpm typecheck` 通过 —— 本地与 CI 均已验证                       |
| P2           | T05–T09 | ✅ 单测全绿；T05–T09 各模块语句与行覆盖 100% |
| P3           | T10–T11 | ✅ 一条必然失败的命令产出 `E-0001` —— 已在临时知识库上端到端验证；99% 门槛现在由 `pnpm test` 执行 |
| P4           | T12–T14 | ✅ 重复的失败被自动注入，模型不再重新诊断 —— T12（通知与上限）、T13（四个注入点）与 T14（解决检测）已完成；在真实会话里看到模型不再重新诊断要等 T17 |
| P5           | T15     | ✅ 模型可调 `err_lookup` 与 `err_record` —— 已通过插件自己的钩子与工具在临时知识库上验证：记下的解法以 `fixed` 写进 `ERRORS.md`，能用原始报错查到，并随下一次失败的通知送达；真实会话要等 T17 |
| P6           | T16–T17 | `--dump-config` 可见该条目；云端与本地报错各记一条                    |
| P7（可选）   | T18     | 导出文件可读                                                          |

### 没有任何 task 覆盖的部分

有两个缺口，与其藏在勾选框后面，不如明说：

- **发布没有任何 task。** 状态表里写着「发布到 npm」，§17 第 1 问原先没有核实 npm 名（2026-10-01 核查时 `dsh-errkb` 与 `err-kb` 均未被占用），但 T01–T18 里没有一条覆盖名字预留或发布；CI 以仓库基础设施的形式单独落地了（见[目前完成到哪一步](#目前完成到哪一步)）。
- **`err_export` 从 README 的任务清单里掉了。** 设计文档 §15 的 P7 在 Obsidian 导出之外还提到 `err_export` 单文件 JSON 备份，而 T18 只写了 Obsidian 导出。要么把工具找回来，要么把阶段描述改窄。

### 完整任务分解

| #     | Task                       | 产出                                                                         | 验收                                                               |
| ----- | -------------------------- | ---------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| ☑ T01 | 初始化 `package.json`      | 包名、入口、bundle patch、peer 依赖                                          | `pnpm install` 无错                                                |
| ☑ T02 | 配置 tsconfig 与 tsdown    | `tsconfig.json`、`tsdown.config.ts`                                          | `pnpm build` 产出 `lib/index.js`                                   |
| ☑ T03 | 配置 vitest 与覆盖率门槛   | `vitest.config.ts`、`tests/`                                                 | `pnpm test` 跑通                                                   |
| ☑ T04 | bundle patch 与空 `apply`  | 插件入口：`name`、`inject`、`Config`、`apply`                                | `pnpm typecheck` 通过                                              |
| ☑ T05 | 解析库路径                 | `src/paths.ts` —— 三级解析、启动日志                                         | 三种情形各返回预期路径                                             |
| ☑ T06 | 规范化与指纹               | `src/signature.ts`                                                           | 路径、行号、PID、时间戳、UUID 变化 → 指纹不变；不同报错 → 指纹不同 |
| ☑ T07 | 脱敏                       | `src/redact.ts`                                                              | 断言输出中 0 命中                                                  |
| ☑ T08 | 文档存储                   | `src/store.ts` —— 解析、渲染、追加、归档、锁、原子写                         | 写读往返一致；手改的解法能读回；并发 50 次记录产出 50 个唯一编号   |
| ☑ T09 | 匹配                       | `src/match.ts` —— 精确、模糊、code 兜底、误判兜底                            | 边界值 0.71 / 0.72 / 0.73 符合规格                                 |
| ☑ T10 | 分类与噪声抑制             | `src/capture.ts`                                                             | 瞬时错误达阈值前不编号；关闭采集则零写入                           |
| ☑ T11 | 接前两个钩子               | `agent/error`、`tools/result` 监听器                                         | 一条必然失败的命令产出 `E-0001`                                    |
| ☑ T12 | 生成通知                   | `src/inject.ts` —— 模板、上限、去重                                          | 上限成立；source 形状与 summary 长度精确                           |
| ☑ T13 | 接四个注入点               | `tools/post-execute`、`agent/pre-step`、`agent/session-start`、system prompt | 重复的失败被自动注入，模型不再重新诊断                             |
| ☑ T14 | 解决检测                   | `src/resolve-detect.ts`                                                      | `fixed` 条目提示一次后静默                                         |
| ☑ T15 | 五个工具                   | `src/tools.ts`                                                               | 模型可调 `err_lookup` 与 `err_record`                              |
| ☐ T16 | 监听 `agent/request-error` | 监听器，以及 LLM 失败分类                                                    | 返回值与下游结果对象同一                                           |
| ☐ T17 | 装进 web profile           | `dsh plugin --profile web add .`                                             | `--dump-config` 可见该条目；云端与本地报错各记一条                 |
| ☐ T18 | 可选：Obsidian 导出        | `exportDir` 导出                                                             | 导出文件可读                                                       |

## 开发

骨架已就位（T01–T04），下面这些命令都能跑。`build`、`typecheck`、`lint`、`test`、`format:check` 正是 CI 在每次推送到 `main` 与每次 PR 上执行的五步：

```sh
pnpm install
pnpm build            # tsdown → lib/
pnpm test             # vitest + 覆盖率；statements 或 lines 低于 99 即失败
pnpm typecheck        # tsc --noEmit
pnpm lint             # oxlint
pnpm format           # prettier --write .
pnpm format:check     # prettier --check .（CI 跑的就是这条）
```

### 测试计划

测试计划在设计文档 §14。覆盖率门槛：statements 与 lines ≥ 99，自 T11 起由 `pnpm test` 强制执行。计划分七组：

1. `signature` —— 路径、行号、PID、时间戳、UUID 变化后指纹不变；不同报错指纹不同；空串、纯 ANSI、超长串不崩。
2. `match` —— 精确命中、Jaccard 边界（0.71 / 0.72 / 0.73）、短消息 code 兜底、误判条目永不注入。
3. `redact` —— `sk-`、`Bearer`、`api_key=`、长 hex、邮箱、绝对路径全部被替换；断言输出中 0 命中。
4. `store` —— 写入/解析往返一致；手改的解法能读回；损坏文件走「另存 + 只追加」；归档阈值触发；编号单调递增；**并发 50 次记录产出 50 个唯一编号，且文档仍可完整解析**。
5. `capture` —— 四类载荷分类正确；瞬时限流达阈值前不编号；采集开关全关时零写入。
6. `inject` —— 文本长度上限、每步 ≤1 条、每回合 ≤3 条、source 形状 `{kind:'plugin',plugin:'err-kb',form:'notice',summary}` 且 `summary` ≤120 字符。已在 `tests/inject.test.ts`（T12）里，另含：中文为主的长解法、同编号每会话 ≤2 次、`fixed` 条目只提示一次、各 `inject` 模式下的近似命中与未命中措辞、不可注入条目静默、解法信任的措辞切换与复发 2 次后停止。
7. `plugin` —— 用假 ctx 校验监听器已注册、异常被吞、`agent/request-error` 返回值与下游结果完全一致（对象同一性断言）。前两项已在 `tests/plugin.test.ts`（T11）里，外加「失败命令产出 `E-0001`、重复一次命中数加一」；`agent/request-error` 那一项随 T16 到来。`tests/injection.test.ts`（T13）扮演宿主来驱动四个注入点：重复的失败在上限之内把已知解法放进 `additionalContexts`、死掉的回合的通知随下一次 `agent/pre-step` 送达、每种 `sessionDigest` 模式、常驻段随 `systemPromptHint` 出现或消失、`inject: 'off'` 时不出声而采集照常、两个 waterfall 原样返回下游结果（对象同一性断言），我们内部抛错时也一样。

第 4 组里那条并发测试是最要紧的一条，因为它对应的是唯一权威文件被写坏的失败模式。

## 常见问题排查

下面全部是「设计规定的行为」，不是「实测到的现象」—— 现在还没有东西能跑。

| 症状                                      | 该查什么                                                                                                                     |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 「它到底装上了没有？」                    | 启动日志那一行会打印解析出的库路径；`dsh --profile web --dump-config` 能看到 `err-kb` 条目。装完必须重启 —— `dsh.profile.bundles` 不热重载 |
| 「我的报错去哪了？」                      | `err_stats` 会打印解析出的路径。先看[知识库落在哪里](#知识库落在哪里)                                                         |
| 「什么都没被记录。」                      | 查 `capture` 开关、`captureExitCodes`，以及这次失败是不是瞬时类（`RATE_LIMIT`、`SERVER`、`TIMEOUT`、`TRANSPORT`、`EMPTY_RESPONSE`）—— 那些按会话在内存里计数，累计到 `transientThreshold` 才升级；条目建好之后，它的重复计在 `state.json` 里 |
| 「写入被静默跳过了。」                    | 这正是库目录不可写、拿不到锁、或写入 3 次重试 + 500 ms 后超时时的既定行为。检查库目录权限，以及是否已回退到 `$DSH_HOME/errkb/` |
| 「注入太吵了。」                          | `inject: 'off'` 彻底关掉注入而采集继续；`systemPromptHint: false` 去掉常驻的 50 token 段；`sessionDigest: 'off'` 去掉开场摘要   |
| 「某条条目一直带着错误解法被注入。」       | 用 `err_record` 把它设成 `wontfix`，或标记为误判 —— 它不再被自动注入，但仍在计数                                              |
| 「`ERRORS.md` 看起来坏了。」              | 插件永不回写自己解析不了的文档：原文件会另存为 `ERRORS.corrupt-<时间戳>.md`，新条目只追加在其后。修好另存的那份再放回去         |
| 「我手改了某条解法，却没生效。」          | 手工修改优先于索引，本不该发生 —— 检查改动是否落在 `- Fix:`（或 `- 解法:`）字段内、且在下一个字段名之前，然后删掉 `errors.index.json` 强制重建 |
| 「我想从头来过。」                        | 删 `state.json`（本机的命中与解法信任计数：计数回到 `ERRORS.md` 里写的命中数，信任从头算）、删 `errors.index.json`（缓存），两者都会重新生成。删 `ERRORS.md` 就是删知识 —— 那才是唯一要紧的文件      |
| 「两台机器的计数对不上。」                | 预期如此：计数器是本机的，刻意不进 git。共享的只有知识                                                                        |

## 卸载

**尚未规定。** 设计文档没有覆盖卸载，也没有对应的 task —— 见[待拍板的事项](#待拍板的事项)。

目前确定的事实是：安装插件会往 `dsh.profile.bundles` 里加一条（通过 bundle patch），所以干净的卸载就是移除那条并重启 `dsh web`。知识库不过是一个目录里的若干文件，插件对它只做追加；插件不会删除它，所以要备份或删除请自己动手。

## 待拍板的事项

**已定 2 问，剩 6 问。** 第 2 问与第 4 问已为 T07–T08 作答，并记入 §17。其余几问仍决定后续 task 做什么：第 5 问（复用范围）影响 T09 的匹配，第 6 问（`captureExitCodes`）影响 T10 的分类（见[阶段对照](#阶段对照)）。答案写在 §17 的「你的回答」列里，或者随便写在 [`docs/设计说明书.md`](docs/设计说明书.md) 的 §19 批注区。

| # | 问题                                              | 建议默认值                                                          | 状态 |
| - | ------------------------------------------------- | ------------------------------------------------------------------- | ---- |
| 1 | 包名 / 插件 id / 工具前缀 / 编号前缀              | `dsh-errkb` / `err-kb` / `err_` / `E-` —— 2026-10-01 核查时两个 npm 名均未被占用（registry 404） | 未定 |
| 2 | 文档字段名用什么语言                              | 字段名中文 + 机器 key 英文                                          | **已定：** 默认英文字段名，`labels: 'zh'` 切中文；两种都解析 |
| 3 | 接受「计数器不进 git、只同步知识」                | 接受                                                                | 未定 |
| 4 | `errors/` 是否直接提交进 GitHub 仓库              | 是，以强制脱敏作为兜底                                              | **已定：** 否 —— 真实知识库放私有仓库（`kbDir` 指向它）；本仓库忽略 `errors/`，只放精选 `seeds/` |
| 5 | 复用范围                                          | 所有项目共用一个库，用条目里的 project 字段区分来源                 | 未定 |
| 6 | `captureExitCodes` 默认开还是关                   | 开 —— 命令失败是最常见的复用场景                                    | 未定 |
| 7 | P7 要哪些（GUI 面板 / 自动执行修复命令 / Obsidian 导出） | 先只要 Obsidian 导出                                          | 未定 |
| 8 | 包根落点                                          | `<repo-root>`                            | 未定 |

还有两个缺口**不在** §17 里，但在 README 能不再写「尚未规定」之前，它们同样需要一个答案：

- **环境要求**：Node 与 pnpm 已在 `package.json` 固定（`engines.node >=22.13`、`packageManager pnpm@11.7.0`），但仍没有 OS 支持范围声明。
- **分发与维护**：没有任何 task 覆盖 npm 发布、npm 名占用核查、CI，以及卸载。

## 后续想法

记录在设计文档 §18。一条都没排期；T18 随时可以砍掉，不影响主线。

1. **把「回收」做成「预检」（防错 > 记错）** —— 用 `tools/pre-execute` 加一道 guard，认出「已知必败」的命令并连同已记录的解法一起拦下，把一个 3 轮失败循环压成 1 条消息。它与 `dsh-repeat-tool-reminder` 互补：那个管「重复」，这个管「已知必败」。
2. **`err_fix` —— 一键复现修复方案** —— 条目里若记下的是确定性命令，提供 `err_fix <id>` 经审批后重跑，真正闭环「回收 → 再利用」。需要接 `dsh-user-approval`，所以默认不开。
3. **把错误库同时做成一个 skill** —— 借 `dsh-skill` 的文件系统发现能力把库暴露为 `SKILL.md`，模型按需读取相关段落，而不用一次吃满上下文。
4. **环境指纹 + 解法可信度** —— 每条记录 os / node / pnpm / shell / provider / 模型版本；在其他设备命中且环境指纹不同时，通知里加一句「可能不适用」，避免拿 A 机的解法坑 B 机。
5. **天然对压缩友好** —— `dsh-compaction-tool-result-pruner` 会丢旧工具结果，但这些事实**在磁盘上**、需要时再注入；这是本设计相对「把解法留在对话历史里」的结构性优势。
6. **token 账本可视化** —— 接 `@deepseek-ai/dsh-token-meter` / `dsh-session-stats`，让 `err_stats` 输出「注入 0.9k / 机制上省掉的下限 8k」。没有数字，阈值就无从调起。
7. **内置种子条目** —— 把 `CONTEXT_OVERFLOW`、`NO_ADAPTER`、Windows 中文路径 EPERM 这类「必踩一次」的坑预置成条目，让全新安装第一天就有价值。
8. **团队共享** —— 因为文档进 git，把 `errors/` 放进团队仓库就等于一个共享踩坑库；再加一层 `err_promote`（本地条目提升为公共条目）就能做知识沉淀流程。
9. **Web GUI 面板** —— 被否掉那个选项的完整版：第三方包可以声明 `dsh.client`（`platform: 'web'`）+ 客户端模块，做列表视图、命中曲线和设置页。代价是要重建 Web 产物，建议 P6 跑稳后再评估。
10. **反向利用** —— `err_list --status open` 就是一份现成的「这个项目当前还没解决的坑」，可以直接当 issue 草稿或新人上手文档。

## 相关项目

2026-09-30 调研了 GitHub 上 [`dsh-plugin`](https://github.com/topics/dsh-plugin) topic 下的项目，2026-10-01 复核。已有好几个插件在采集错误，但没有一个给错误分配跨会话稳定的编号，并在模型开始诊断**之前**把已记录的解法推回上下文。

| 项目 | 做什么 | 区别 |
| ---- | ------ | ---- |
| [166767/dsh-error-audit](https://github.com/166767/dsh-error-audit) | 监听同样的三个钩子（`agent/error`、`agent/request-error`、`tools/result`），把每个错误写进 `$DSH_HOME/error-audit/`，通知模型，并提供 `read_error_logs` 工具 | 是日志而不是知识库：不去重，也不存解法 |
| [d86e/dsh-doctor](https://github.com/d86e/dsh-doctor) | 采集失败的工具结果，分为 transient / agent / business 三类 | 只观察和分类，从不注入解法 |
| [Wanbinyu/dsh-error-lens](https://github.com/Wanbinyu/dsh-error-lens) | 对 provider 错误（401、403、429、上下文溢出等）做脱敏后的诊断 | 只在会话内，不落盘。它的 README 明说基于文本规则的脱敏不可能完整 —— 本项目 §4.4 也受同样的限制 |
| [Leitarkkk/dsh-research-nudge](https://github.com/Leitarkkk/dsh-research-nudge) | 用同一个注入点（`tools/post-execute` → `additionalContexts`），在失败、重复和工具调用累积到阈值后提醒模型去查资料 | 只在会话内；可能和 `err-kb` 的通知在同一步触发 |

`dsh` 之外的相邻项目：

- [vshulcz/deja-vu](https://github.com/vshulcz/deja-vu) —— 本地搜索历史 agent 会话，不调用 LLM。
- [thedotmack/claude-mem](https://github.com/thedotmack/claude-mem)、[MemTensor/MemOS](https://github.com/MemTensor/MemOS) —— 通用 agent 记忆；两者都要花模型调用（摘要或 embedding）来构建记忆。
- [ankitkr3/compounded](https://github.com/ankitkr3/compounded) —— 学到的经验随干净的使用积累信任，带偏任务时降级。
- [Sentry 事件分组](https://docs.sentry.io/concepts/data-management/event-grouping/) —— 错误指纹与分组的成熟先例。

`dsh-errkb` 的定位：**稳定、人类可读的编号**（`E-0007`），跨会话、跨机器不变；**解法在诊断之前注入**，不超过 120 token；**Markdown 是唯一真源**，可手工编辑、由 git 合并；**零模型调用** —— 采集、匹配、注入全是本地文本处理。

## 参与与反馈

T01–T15 已实现并有测试；T16 与 T17 仍开放，插件还没装进任何地方，也没有发布。欢迎贡献 —— 修 bug、补测试、审设计，或者趁 §17 未定的事项改起来还便宜时对设计提异议。

- **设计文档是唯一真源。** 如果本 README 和 [`docs/设计说明书.md`](docs/设计说明书.md) 冲突，以设计文档为准（并且本 README 有 bug 值得报）。
- **意见写进文档。** §17 是一张留了空「你的回答」列的表，§19 是一块批注区，都是给人直接写进去的。
- **两份 README 必须同步。** `README.md` 是英文，`README.zh-CN.md` 是中文，而它们已经漂移过一次。改其中一份，就要在同一次改动里改另一份。
- **仓库的语言约定**：`README.md` 以及代码、注释、提交信息、文档用英文；`README.zh-CN.md` 与设计文档用中文（设计文档用中文是刻意的选择）。
- **`pnpm test` 执行覆盖率门槛。** 它带覆盖率跑 vitest，语句或行低于 99% 即失败；推送前连同 `pnpm typecheck`、`pnpm lint`、`pnpm format:check`、`pnpm build` 一起跑 —— CI 跑的就是这五步。
- **隐私检查必须通过。** `.github/workflows/privacy-guard.yml` 会拒绝整个仓库里出现的用户绝对路径、个人邮箱与形似凭据的字符串；请改用 `<repo-root>` 之类的占位符。
- **一个 task 一个提交**，用约定式前缀并带上 task 标签：`feat: add the five err_ tools (T15)`、`fix: … (T15)`、`docs: …`。

## 许可证

[MIT](LICENSE)

---

<sub>DeepSeek Harness 的第三方插件，与 DeepSeek 官方无关。</sub>
