# 项目当前状态

[English](project-status.md)

状态日期：2026-10-22

本文只回答一个问题：**这个项目现在有哪些内容可以被信任和使用？** 实施计划描述目标与施工顺序；本文记录当前真实的交付状态。如果本文与早期完成记录发生冲突，应重新实测，并在同一次修改中同步更新两份文档。

## 一句话结论

错误知识库已经不只是 DSH 插件，它的核心架构是一个本地、多宿主的错误复用平台：

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

核心、数据库、CLI、后台服务、MCP 服务和首批宿主连接器已经基本实现。但是项目**还不是一个完成的 Windows 工具箱产品**，因为可执行文件、启动器、安装器和发布包尚未统一到同一个可追踪版本。

## 状态用语

| 用语 | 含义 |
| --- | --- |
| 已实现 | 已有源码和自动化测试 |
| 已验证 | 已在注明的真实环境中成功运行 |
| 已验收 | 任务专属门槛、公共门槛和必要人工检查全部通过 |
| 可发布 | 同一个带版本产物已通过干净机安装、启动、重启、升级和卸载 |

“已实现”不自动等于“已验证”“已验收”或“可发布”。

## 已经实现的内容

- 确定性的分类、标题提取、脱敏、规范化与指纹计算。
- PostgreSQL 迁移 `0001`–`0008`、幂等事件记录、解法版本、结果反馈、可信度计算、精确检索与 trigram/Jaccard 检索。
- 使用 Named Pipe API、健康文件、脱敏日志和单实例锁的 Windows 后台服务。
- 搜索、查看、记录、列表、反馈、统计、命令包装、数据库迁移/状态、doctor、MCP 和版本信息等 CLI 命令。
- MCP stdio 服务，以及兼容矩阵中记录的 DSH、Codex CLI、Claude Code 连接路径。
- 原生托盘/搜索模型，以及问题详情、设置、统计、审计和通知服务模型。
- 安装器源码中的 schema 迁移与当前用户 HKCU 登录自启动。

## 已经验证的内容

| 范围 | 证据级别 |
| --- | --- |
| TypeScript 适配器 | 2026-10-22 本机检查：733 项测试通过，语句/行覆盖率 99.4% |
| 不强制依赖真实数据库的 Go 包 | 本机 `go test ./...`、`go vet ./...` 和全新构建通过 |
| CUE 契约 | 本机 `cue vet ./spec/...` 通过 |
| 仓库格式 | 2026-10-22 的 `pnpm format:check` 已通过；历史任务卡按归档处理 |
| 通用 CLI 闭环 | 记录在 [`e2e-cli-loop.zh-CN.md`](e2e-cli-loop.zh-CN.md)；真实数据库测试需要 `ERRKB_TEST_DATABASE_URL` |
| DSH / Codex / Claude 连接器 | 按 [`connector-matrix.zh-CN.md`](connector-matrix.zh-CN.md) 的固定版本实测；未测字段继续明确标记 |
| 安装迁移与登录启动 | 已在开发机演练；仍缺干净标准用户 VM 证据 |

没有设置 `ERRKB_TEST_DATABASE_URL` 时，依赖数据库的 Go 测试会跳过。因此，缺少该变量时的绿色单元测试不能证明完整的存储与检索闭环。

## 当前发布阻塞项

1. **本地目录不是 Git 工作区。** 当前目录没有 `.git`，构建只能显示 `commit local` 或 `commit unknown`，无法可靠回滚，也无法安全追踪本地独有的 UI 修改。
2. **Web 工具箱不在远端当前启动路径中。** 本地有 `cmd/errkb/web.go`、嵌入式资源和 `ui --web` 分派，但 GitHub 当前 `main` 与现有发布二进制都会拒绝 `--web`。
3. **安装器没有工具箱启动入口。** `installer.iss` 没有 `[Icons]`；`start-errkb.cmd` 也没有进入安装目录或当前发布包。
4. **UI 不能可靠恢复已安装的后台服务。** pipe 不可达时，Web 壳只使用 `ERRKB_DSN`/`--dsn` 启动 daemon；它不会读取 `%LOCALAPPDATA%\errkb\pg\credentials.json`，默认迁移路径还依赖当前工作目录。
5. **发布产物互不一致。** `dist\errkb.exe`、已安装 EXE 与 `dist\release\errkb-0.1.0\errkb.exe` 来自不同修订；发布目录中的 EXE 连 `errkb db` 都没有。
6. **Web UI 目前只有搜索。** 问题详情、编辑、统计、审计与设置路由/页面仍未实现。
7. **干净 VM 验收仍阻塞。** 标准用户安装、真实重启、工具箱启动和两种卸载路径仍需证据。

## 当前安全使用方式

### 开发使用

使用当前源码构建的二进制、PostgreSQL 16 和显式 DSN：

```powershell
go build -o errkb.exe ./cmd/errkb
.\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe doctor --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
```

然后使用 CLI、MCP 服务或已经实测的连接器。

### 最终用户安装

目前没有受支持的最终用户安装版本。现有 setup 文件是诊断/验收产物，不是正式发布。在同一次干净构建通过下方清单之前，不应以 v0.1.0 名义发布或分发。

## 到达可发布工具箱的施工顺序

1. 从 GitHub 当前 `main` 恢复真实 Git 工作区，把本地独有 UI 修改保存到具名分支。
2. 完成 EKB-057：接通 `ui --web`、基于安装凭据的 daemon 启动、搜索冒烟测试和便携工具箱包。
3. 完成 EKB-058：Web 壳中的问题详情与解法编辑。
4. 完成 EKB-059：Web 壳中的统计、审计以及连接器/隐私设置。
5. 完成 EKB-060：安装器文件、开始菜单快捷方式、便携 ZIP 与干净 VM UI 证据。
6. 使用真实 PostgreSQL 跑完全部门槛，并清除未跟踪探针产物。
7. 从一个带 tag 的提交只构建一次，确认 EXE、安装器、manifest、校验和与安装后文件指向同一修订。

## 发布检查表

只有以下答案全部为“是”，才可以发布：

- `errkb version` 是否显示目标 tag 和 commit？
- 安装器里是否就是这个 EXE？
- 开始菜单快捷方式是否能在不显示终端的情况下打开工具箱？
- 工具箱是否能使用安装凭据启动或重新连接受管 PostgreSQL 与 daemon？
- 搜索、详情/编辑、统计/审计和设置是否全部通过本地 pipe 工作？
- 同一个已知错误是否能在不调用模型的情况下返回已记录解法？
- 同一标准用户真实重启后，集群与 daemon 是否恢复？
- 保留数据和删除数据两条卸载路径是否符合文档？
- 数据库集成测试、格式、隐私检查与发布哈希是否全部通过？
- 中英文文档是否同步？

## 文档职责

| 文档 | 用途 |
| --- | --- |
| `project-status.zh-CN.md` | 当前交付事实与阻塞项 |
| `workflow/README.zh-CN.md` | 活动问题、文件修复顺序与清理决定的控制中心 |
| `features-and-implementation.zh-CN.md` | 用户功能及其源码、契约与数据实现链路 |
| `implementation-plan.zh-CN.md` | 产品决定、任务顺序与验收标准 |
| `toolbox-ui.zh-CN.md` | UI 架构与 EKB-057–060 范围 |
| `acceptance-vm.zh-CN.md` | 干净机证据与剩余人工门槛 |
| `connector-matrix.zh-CN.md` | 宿主/版本能力证据 |
| `README.zh-CN.md` | 项目简明入口 |

