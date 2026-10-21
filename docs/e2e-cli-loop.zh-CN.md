# CLI 技术错误闭环（EKB-035）

[English](e2e-cli-loop.md)

状态：**2026-10-19 已通过**。环境为 Windows 11 10.0.26200、PostgreSQL 16.15（`127.0.0.1:5432`）以及从当时源码构建的 `errkb dev`。

> 这是 EKB-035 时点的历史验收记录，不等于当前 `dist`、已安装 EXE 和旧发布目录已经一致。当前交付状态见 [project-status.zh-CN.md](project-status.zh-CN.md)。

该 P4 端到端场景使用真实失败命令、生产后台服务、生产 Named Pipe 客户端、真实 PostgreSQL 与真实检索链路。没有 mock，也没有硬编码返回值；解法通过产品自己的 solution API 写入，再由检索器从数据库读回。

可重复脚本为 `tests/e2e/cli_loop_test.go`：

```powershell
go test ./tests/e2e/ -run TestCLIErrorLoop -v
```

只有当 `ERRKB_TEST_DATABASE_URL` 指向允许创建 schema 的数据库时才运行。设置 `ERRKB_E2E_ARTIFACT_DIR=<dir>` 可输出验收产物；测试会断言本文列出的事实。

## 环境

| 项目 | 值 |
| --- | --- |
| 系统 | Windows 11 Pro for Workstations 10.0.26200 |
| 数据库 | PostgreSQL 16.15，`pg_trgm` 1.6 |
| 二进制 | `errkb dev`（go1.26.4，windows/amd64） |
| 传输 | 只使用 Named Pipe（`\\.\pipe\errkb-e2e-…`），不监听 TCP |
| 模型调用 | **无** |
| 网络 | 只有本机 PostgreSQL，无外连 |

闭环开始前，脚本要求 `doctor` 的八项检查全部通过；环境退化时会带失败项名称跳过。

## 不依赖模型

- `go.mod` 只依赖 `pgx`、`go-winio`、`golang.org/x/sys` 和 `yaml.v3`；`go list -deps ./cmd/errkb` 不含模型 SDK。
- CLI 通过本地 Named Pipe 通信，后台服务只连接 PostgreSQL。
- 输出中的解法来自 `solution_versions` 数据行，不是生成文本。

## 场景一：首次失败创建问题，但不返回解法

```text
errkb run --connector <connector-id> --session <session-id> --project e2e --json -- cmd /c type <missing-path>
```

`type` 读取不存在的路径后失败，CLI 原样传递退出码并记录失败。返回内容包含：

- `record.created = true`；
- 新的 `event_id`、`occurrence_id`、`issue_id` 与指纹；
- `lookup` 通过 `exact_fingerprint` 找到刚创建的问题；
- **不存在 `solution` 对象**，因为数据库中还没有解法。

测试同时断言数据库只有一个 issue，并存在记录该指纹原因的 retrieval 行。若第一次就返回解法，该门槛必须标记为阻塞；实测没有发生。

## 场景二：相同失败返回已知解法

随后通过产品 API 执行 `CreateSolution` → `AddSolutionVersion` → `TransitionSolution(verified)`，记录下列解法，而不是直接写入测试输出：

> 在资源管理器中打开缺失路径，确认驱动器和目录存在，然后重新运行命令。

再次运行相同失败命令后，返回同一个 issue id，并展示数据库中的原始英文解法、`source e2e`、`applies to os=windows` 和 SQL 函数 `errkb_trust()` 计算的可信度 0.50。它证明答案来自可追踪的数据行，而不是模型临时生成。

## 场景三：记录使用结果

```text
errkb feedback <issue-id> <solution-version-id> --outcome success --json
```

结果返回新的 `outcome_id` 与 `recorded: true`，并在数据库写入 `success`。

## 场景四：近似重复只能作为候选

测试预先加入词语相近、规范化文本不同的错误：

```text
The system cannot find the specified file.
```

搜索原始消息时，两条结果都标记为 `jaccard_rerank`、`approximate: true`、置信度 0.55；人类可读输出明确显示：

```text
reason jaccard_rerank (APPROXIMATE - confirm before reuse)
```

没有任何候选声称 `exact_fingerprint`。记录成功结果后，解法可信度由 0.50 变为 0.60，说明 `errkb_trust()` 在查询时根据结果重新计算。

## 最终数据状态

`errkb stats --json` 返回：

```json
{ "totals": { "events": 3, "issues": 2, "solutions": 1, "outcomes": 1 } }
```

数据库中有两条问题（原始错误命中两次、近似错误一次）、三条事件、一条已验证解法、一条成功结果，以及分别标记 `{fingerprint}` 或 `{token_similarity}` 的检索记录。

## P4 证据映射

| 门槛 | 证据 |
| --- | --- |
| 真实场景，不是模拟 | 测试构建 `./cmd/errkb`、启动 daemon、两次运行真实 `cmd /c type <missing>`，并检查输出和 SQL 计数 |
| 首次创建 issue | `record.created = true`，首次运行后 `issues = 1` |
| 首次不返回解法 | `lookup.candidates[]` 不含 `solution`，由测试断言 |
| 第二次从数据库返回已知解法 | 人类输出与 `AddSolutionVersion` 写入的正文完全一致 |
| 检索记录包含原因 | 精确命中用 `{fingerprint}`，近似候选用 `{token_similarity}` |
| 结果被记录 | `outcomes` 存在 success，可信度 0.50 → 0.60 |
| 近似匹配需要确认 | JSON 为 `approximate: true`，文本显示确认警告，且没有精确指纹理由 |
| 无模型、无外网 | 依赖中无模型 SDK；链路只有本地 pipe 与 `127.0.0.1:5432` |
| 可重复 | 测试会从头创建 schema、二进制、daemon 和全部断言 |

## 已知边界

- 本记录对应 EKB-035。当时测试直接调用 `database.Migrate`；EKB-056 后源码增加了 `errkb db migrate`，当前开发与安装流程应使用该命令。测试显式 DSN 是为了隔离。
- 场景运行真实 Windows `cmd /c type`，所以与 daemon/CLI 一样只支持 Windows；Linux CI 会跳过。
- 本记录生成时，`errkb run` 需要显式 `--connector` 与 `--session`；EKB-056 后增加 `cli` 默认值并修复了前导 `--`。
- 因为目前没有录入解法的 CLI 子命令，测试通过 Go API 写入解法。P4 允许这种方式，关键是返回内容必须来自数据库。
