# 无用、未接入与生成文件审计

[English](unused-code-audit.md)

审计日期：**2026-10-22**

本文标注当前运行时不需要的内容，但不会把所有“暂时不可达”的源码粗暴称为垃圾。本文只提供标注和清理依据，本次审计没有删除项目文件。

> **执行说明（2026-10-22）：** 两个意外生成的 Claude 探针文件现已不存在。`coverage/` 与 `dist/` 仍是本地可再生产物，`packaging/windows/cleanup-local.ps1` 的预览模式仍会列出它们。目前没有生产源码被标为 `DELETE-SAFE`。

## 标记定义

| 标记 | 含义 | 默认处理 |
| --- | --- | --- |
| `DELETE-SAFE` | 本地临时或生成内容，不承担源码、测试夹具或发布依据职责 | 可以删除 |
| `REGENERATABLE` | 可由明确命令重新生成的构建、覆盖率或依赖目录 | 不进入源码管理，需要时重建 |
| `STALE-ARTIFACT` | 已经不能代表当前同一源码修订的生成产物 | 禁止运行或发布，干净构建前移除 |
| `DORMANT-CODE` | 有测试，但当前生产入口没有调用的实现 | 完成对应接入决定或任务前保留 |
| `TEST-COMPAT` | 只服务于测试、升级承诺或历史兼容，不属于普通运行路径 | 除非明确取消兼容承诺，否则保留 |
| `ACTIVE` | 构建、发布包、当前运行链路或文档流程需要 | 保留 |

## 审计方法和证据边界

本次使用以下依据：

- 通过 `package.json`、`tsdown.config.ts`、Go module 信息和打包脚本确定入口；
- 使用 `go list ./...` 检查包依赖；
- 全仓库搜索非测试生产调用；
- 构建当前 `cmd/errkb` 并检查符号，作为辅助证据；
- 统计本地文件大小，并结合此前已确认的源码、根 `dist`、已安装 EXE 与 `dist/release` EXE 行为差异。

DSH 包入口是 `src/index.ts`，Go 程序入口是 `cmd/errkb`。Go 函数仅仅能够编译或拥有测试，不代表用户可以调用；它还需要生产调用者或对外包契约。反过来，测试夹具不被 EXE 读取，也不代表它是垃圾。

## A. 已确认可清理内容

这些内容不包含不可替代的源码实现。

| 路径 | 标记 | 证据 | 建议 |
| --- | --- | --- | --- |
| `CUsersADMINI~1AppDataLocalTempclaude-probefired.txt` | `DELETE-SAFE` | 只包含 `cmd.exe` 标题和一次 Claude `SessionStart` 探针输出；全仓库无引用 | 删除 |
| `CUsersADMINI~1AppDataLocalTempclaude-probefired2.txt` | `DELETE-SAFE` | 同一临时 Hook 探针的第二份输出；全仓库无引用 | 删除 |
| `coverage/` | `REGENERATABLE` | Vitest HTML/LCOV 报告；已被 `.gitignore` 忽略；`pnpm test` 可重建 | 非覆盖率检查期间删除 |
| `dist/inno-temp/` | `DELETE-SAFE` | 本地 Inno Setup 探针、测试安装器与日志 | 下次发布构建前删除 |
| `dist/winres-a/` | `DELETE-SAFE` | 一次可复现性比较输出，打包流程不消费 | 记录比较结果后删除 |
| `dist/winres-b/` | `DELETE-SAFE` | 第二份可复现性比较输出，打包流程不消费 | 记录比较结果后删除 |
| `dist/errkb-console.cmd` | `DELETE-SAFE` | 针对过期根二进制的临时帮助脚本；打包流程无引用 | 删除 |
| `dist/errkb-shell.cmd` | `DELETE-SAFE` | 临时诊断 shell；安装器和发布脚本无引用 | 删除 |

其中两个根目录 Claude 探针文件最符合“完全无用”：它们是带错误绝对路径文件名的意外命令输出。

## B. 必须整体替换的过期产物

| 路径 | 标记 | 当前为什么不安全 | 正确处理 |
| --- | --- | --- | --- |
| `dist/errkb.exe` 以及根安装器、校验和和资源文件 | `STALE-ARTIFACT` | 与 release 子目录处于不同实现时点，且二进制没有本地 Web 启动路由 | 从带 tag 提交重建前移除 |
| `dist/release/errkb-0.1.0/` | `STALE-ARTIFACT` | 旧发布包；其 EXE 命令面与根 EXE、当前源码都不同 | 若需要历史证据则移到仓库外归档，否则本地删除 |
| 整个 `dist/` | `REGENERATABLE` | 已被忽略的构建输出，约 53 MB，内部版本不一致 | WF-012 前清空，再从已验收 tag 只构建一次 |

禁止从这些目录挑选部分二进制，再与新生成的安装器文件混用。下一份可验收发布必须让 EXE、安装器、manifest、SBOM 和校验和来自同一个带 tag 提交。

## C. 本地缓存，不是垃圾代码

| 路径 | 标记 | 作用 | 建议 |
| --- | --- | --- | --- |
| `node_modules/` | `REGENERATABLE` | 本地依赖，约 119 MB | 可删除以释放空间；使用 `pnpm install --frozen-lockfile` 恢复 |
| `lib/` | `ACTIVE` | 发布用 DSH bundle；`package.json.exports` 和 `files` 指向它 | 保留供包发布/商店审核；源码变化后用 `pnpm build` 重建 |
| `lib/index.js.map` | `REGENERATABLE` | 已被 `.gitignore` 忽略的本地 sourcemap | 本地可选；不要因此删除 `lib/index.js` 或 `lib/index.d.ts` |

## D. 原未接入代码——2026-10-22 已复核

初次审计中的大部分条目已经进入生产路径，不得删除：

| 区域与路径 | 当前生产路径 | 标记 |
| --- | --- | --- |
| 详情/设置/统计模型与编辑服务 | `internal/webshell` → daemon API/backend → `NewDetailService`、统计、审计与设置 | `ACTIVE` |
| 通用日志 Tailer | `errkb tail --file` → `cmd/errkb/tail.go` → `capture.NewTailer` | `ACTIVE` |
| 旧 Markdown 交换 | `errkb markdown import|export` → `cmd/errkb/markdown.go` | `ACTIVE` + `TEST-COMPAT` |
| PostgreSQL 管理器与备份/升级辅助 | `errkb pg` → `service.NewPGManager`；`postgres.ps1` 现在只负责转换安装器参数 | `ACTIVE` |

目前真正未接入的只剩 `internal/ui/notify.go` 与 `internal/service/notifications.go`：两者有测试，但生产路径没有构造 `NewNoticeService` 或 `NewNotification`。在桌面通知被正式接入或明确移出范围前，将它们保留为 `DORMANT-CODE`。不得再把已接入的 UI、tail、Markdown 或 PostgreSQL 代码列为清理候选。

## E. 不能误删的历史和测试材料

| 路径 | 标记 | 保留原因 |
| --- | --- | --- |
| `db/migrations/testdata/upgrade/` | `TEST-COMPAT` | 用于证明升级的真实旧 schema、配置、Markdown 和能力文件 |
| `seeds/ERRORS.seed.md`、`seeds/ERRORS.seed.json` | `TEST-COMPAT` | TypeScript 与 Go 兼容测试使用的源 seed 与生成 seed |
| `spec/corpus/` | `TEST-COMPAT` | 跨语言一致性和零错误合并语料 |
| `.md workflow/` | WF-011A/WF-011B 通过后为 `DELETE-SAFE` | 四张卡已被实施计划和工作流文档中的长期证据取代 |
| `connectors/*/probe.md`、`capability.json` | `TEST-COMPAT` | 宿主支持声明的证据 |
| `docs/*.md` 中英文组合 | `ACTIVE` | 用户要求的双语项目、状态和实现文档 |

2026-10-22 已在确认长期证据写入 `implementation-plan.*` 后删除 56 份已完成 EKB 卡片。最后 4 张本地卡片可在 WF-011A/WF-011B 变绿后删除；WF-012 由 `current-workflow.*` 跟踪。

## F. 不应删除的活动代码

- `src/`：所有 TypeScript 模块都能从 `src/index.ts` 的 DSH bundle 导入链到达。
- `cmd/errkb/`：命令分派和当前所有 CLI 模式。
- `internal/core/`、`internal/redact/`、`internal/contract/`：共享确定性处理和契约。
- `internal/database/` 和 `db/migrations/` 中活动的记录、检索和迁移代码。
- `internal/service/api.go`、`backend.go`、`client.go`、`daemon.go`、`doctor.go`、`pipe.go`、`queue.go`、`search_api.go`。
- `internal/ui/status.go`、`tray.go`、`omnibox.go`、`omnibox_win.go`。
- `internal/mcp/` 和 `internal/webshell/`。
- `packaging/windows/`：负责生成、安装和验收 Windows 产物；WF-011A/WF-011B 会验证其生命周期与恢复。

## 建议清理顺序

先预览将处理的固定目标：

```powershell
pwsh -File packaging/windows/cleanup-local.ps1
```

确认四组目标后执行：

```powershell
pwsh -File packaging/windows/cleanup-local.ps1 -Apply
```

1. 删除两个意外生成的 Claude 探针文件。
2. 清理 `coverage/` 和 A 节列出的四类 `dist` 临时/辅助内容。
3. 把剩余 `dist/` 视为无效；只有恢复真实 Git 工作区并选定唯一提交后才重建。
4. 保留已接入的 Web UI、tailer、Markdown 与 PostgreSQL 管理器代码；它们已有生产入口。
5. 单独决定接入还是删除仍处于 dormant 状态的桌面通知模型/服务。
6. WF-011A/WF-011B 变绿后删除四张已被取代的本地 EKB 卡；WF-012 继续保留在实时工作流文件中。

## 删除门槛

源码只有同时满足以下条件，才能从 `DORMANT-CODE` 调整为 `DELETE-SAFE`：

- 全仓库生产引用为零；
- 对外包导出、CLI 命令、安装步骤和兼容承诺均不再需要；
- 对应实施计划任务已经删除或被替代；
- 测试和夹具被有意删除或重定向，而不是被直接弄坏；
- 功能、状态和实施说明的中英文版本同步更新；
- Go 测试、TypeScript 测试/类型检查、CUE 校验和打包检查仍通过。

该门槛可以避免“清理代码”误删那些只是尚未接入的功能实现。
