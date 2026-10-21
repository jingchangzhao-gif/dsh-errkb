# 文件修复顺序

[English](file-repair-sequence.md)

本文把每个活动 `WF-nnn` 问题转换为明确的文件修改顺序。必须按阶段推进，不能因为后续文件容易修改就提前施工；前面的阶段会建立后续 UI 和打包所依赖的契约与运行路径。

## 全局修改协议

处理每个问题时：

1. 在两份 `current-workflow` 中把该行标为 `IN PROGRESS`。
2. 修改代码前先复现现有证据。
3. 按“契约 → 核心/服务 → 界面 → 测试 → 文档”的顺序修改。
4. 每层完成后跑针对性测试，最后跑公共门槛。
5. 把长期证据写入实施计划、项目状态、CHANGELOG 或验收报告。
6. 从两份 `current-workflow` 删除已经解决的行，并把下一条满足依赖的工作改为 `NEXT`。

## 阶段 0——建立安全源码管理

### WF-001：恢复真实 Git 工作区

目的：让后续修改都可审查、可回滚。

文件/流程顺序：

1. 在脏目录之外记录上游 URL 和目标基准提交。
2. 把 `https://github.com/jingchangzhao-gif/error-knowledge-base` 克隆到同级新目录。
3. 核对基准提交和默认分支。
4. 只复制有意保留的本地源码/文档修改；不要复制 `node_modules/`、`coverage/`、`dist/` 或探针输出。
5. 创建具名 EKB-057 分支并检查完整 diff。
6. 后续阶段全部在该 Git 工作区进行。

完成门槛：`git status`、`git diff --stat` 和 `errkb version` 能定位源码状态，同时本地独有 Web 文件仍然存在。

## 阶段 1——删除已确认的生成垃圾

### WF-002：执行清理清单

文件：

1. `packaging/windows/cleanup-local.ps1`
2. `.gitignore`
3. `unused-code-audit.md` 与 `unused-code-audit.zh-CN.md`

执行：

```powershell
pwsh -File packaging/windows/cleanup-local.ps1
pwsh -File packaging/windows/cleanup-local.ps1 -Apply
```

确认两个错误命名的 Claude 探针文件、`coverage/` 和 `dist/` 已消失。不要删除 `lib/`、`node_modules/`、迁移夹具、seed 或休眠源码。

完成门槛：再次预览时所有目标都显示不存在；Git diff 不包含生成产物。

## 阶段 2——完成 Web 工具箱基础

### WF-003：使用安装凭据恢复 daemon 并支持便携启动

修改顺序：

1. `cmd/errkb/web.go`
   - 增加显式 `--credentials`；
   - 解析安装后的默认凭据路径；
   - 优先使用显式 DSN，否则读取安装凭据；
   - 迁移目录相对于 EXE/安装目录解析，不依赖调用者当前目录；
   - 日志中的连接信息始终脱敏。
2. `internal/service/pg_manage.go`
   - 复用 `ReadCredentials`，不要再写第二个解析器。
3. `cmd/errkb/ui_test.go` 或新的 Web 启动测试
   - 覆盖 daemon 已运行、daemon 停止但有凭据、缺少凭据、凭据无效和带空格路径。
4. `internal/webshell/server.go` 与 `server_test.go`
   - 保留仅回环监听、单次令牌、请求体限制和安全响应头；
   - 只有冒烟测试需要时才增加健康/就绪端点。
5. `packaging/windows/start-errkb.cmd`
   - 传递安装凭据/迁移路径，不暴露密码。
6. `docs/toolbox-ui.*`、`docs/install.*`、`docs/project-status.*` 和两份 current-workflow。

完成门槛：从安装目录以外的位置启动时，启动器可以启动或连接 daemon，浏览器搜索能访问真实数据库。

## 阶段 3——安装启动器

### WF-004：安装器与开始菜单入口

修改顺序：

1. `packaging/windows/installer.iss`
   - 安装 `start-errkb.cmd` 和所需资源；
   - 增加 `[Icons]` 开始菜单项与可选桌面快捷方式；
   - 保持标准用户安装和静默安装行为。
2. `packaging/windows/release.ps1`
   - 把启动器及其哈希纳入 bundle、manifest 和校验和。
3. `packaging/windows/vm-acceptance.ps1`
   - 断言安装后的启动器/快捷方式目标和启动行为。
4. `docs/install.*`、`docs/acceptance-vm.*`、`docs/project-status.*`。

完成门槛：标准用户可以从开始菜单无终端打开工具箱，安装后 EXE 身份与 bundle manifest 一致。

## 阶段 4——暴露问题详情和解法编辑

### WF-005 / EKB-058

修改顺序：

1. `spec/local-api.cue` 与契约测试——先定义详情读取和编辑操作。
2. `internal/contract/local_api.go` 及相关类型——校验所有请求/响应，继续明确拒绝不兼容 schema。
3. `internal/service/api.go`、`client.go`、`backend.go`——新操作必须经过 daemon；Web 壳不能直接连 PostgreSQL。
4. `internal/service/edit_api.go`——复用已有历史、审计、确认和只读可信度规则。
5. `internal/webshell/server.go`——增加带令牌、请求体限制和稳定错误 envelope 的详情/编辑路由。
6. `internal/webshell/assets/index.html`、`app.js`、`app.css`——增加详情、版本历史、编辑和确认状态，不自动执行修复。
7. 服务、pipe、Web 路由与浏览器冒烟测试。
8. 双语功能、工具箱、项目状态与 current-workflow 文档。

完成门槛：选择结果可以加载完整历史；新增解法只追加版本；状态/删除需要确认并写审计；可信度保持只读。

## 阶段 5——暴露统计、审计和设置

### WF-006 / EKB-059

修改顺序：

1. `spec/local-api.cue` 和 `spec/config.cue` 中缺少的 wire 操作。
2. `internal/contract/` 请求/响应类型。
3. `internal/audit/log.go`、`stats.go` 及测试。
4. `internal/ui/settings.go` 与对应服务/配置操作。
5. `internal/service/api.go`、`client.go`、`backend.go`。
6. `internal/webshell/server.go` 和路由测试。
7. `internal/webshell/assets/` 页面与 UI 冒烟测试。
8. 双语隐私、工具箱、功能、状态和工作流文档。

完成门槛：实测与估算统计分开；审计筛选可用；配置经过 CUE 校验；删除始终先预览、再确认、后修改。

## 阶段 6——消除生命周期重复实现

### WF-007

对比文件：

- Go：`internal/service/pg_manage.go`、`internal/database/backup.go` 及测试。
- PowerShell/Inno：`packaging/windows/postgres.ps1`、`autostart.ps1`、`installer.iss`、`vm-acceptance.ps1`。

建议选择：安装器编排继续以已经进入生产路径的 PowerShell/Inno 为主，但先把 Go 中独有的备份/回滚安全保证迁入受支持调用路径；之后删除不可达的 Go 生命周期操作，不再长期维护两套实现。

完成门槛：只剩一套安装生命周期路径；安装、升级回滚、保留/删除卸载和凭据权限仍有测试。

## 阶段 7——决定休眠兼容功能

### WF-008：通用日志采集

二选一：

- 通过配置化 daemon/连接器入口接入 `internal/capture/tail.go`，并增加真实宿主验收；或者
- 同时删除组件、测试和“支持通用日志采集”的声明。

不能让一个只有测试、没有用户入口的库继续冒充用户功能。

### WF-009：Markdown 导入导出

二选一：

- 增加由 `internal/database/markdown.go` 支持的正式 `errkb import` / `errkb export`；或者
- 正式结束兼容承诺，并在一次审查中删除实现、seed 生成路径与过期夹具。

只要仍承诺旧版本直接升级，已有升级夹具就必须保留。

## 阶段 8——完整数据库与机器验收

### WF-010：真实 PostgreSQL 门槛

使用可丢弃的 PostgreSQL 16 数据库运行：

```powershell
$env:ERRKB_TEST_DATABASE_URL = '<临时 PostgreSQL DSN>'
go test ./... -count=1
```

同时运行 `docs/benchmarks.*` 与 `docs/e2e-cli-loop.*` 中的性能 smoke、崩溃安全和 CLI 闭环。保存命令、版本、耗时和失败，不保存明文密码。

### WF-011：干净 VM 验收

在干净 Windows 10/11 x64 VM 中以标准用户运行 `packaging/windows/vm-acceptance.ps1`，完成安装、首次启动、真实重启、单实例恢复、知识复用和两种卸载方式。发布包确定前，证据应保存在生成的 `dist/` 之外。

## 阶段 9——一致发布

### WF-012

执行/修改顺序：

1. `packaging/windows/build.ps1`
2. `packaging/windows/installer.iss`
3. `packaging/windows/release.ps1`
4. `packaging/windows/vm-acceptance.ps1`
5. `CHANGELOG.md`、发布说明、安装/状态/验收文档

只从一个已验收 tag 构建一次。核对源码、EXE、安装器、安装后 EXE、manifest 和校验和中的版本、commit 与构建时间。不得复制旧 `dist/` 中的任何文件。

## 公共最终门槛

```powershell
pnpm typecheck
pnpm lint
pnpm test
pnpm format:check
go vet ./...
go test ./...
cue vet ./spec/...
```

发布门槛还要求真实数据库结果、干净 VM 证据以及中英文文档同步。
