# 检索性能与崩溃安全门槛

[English](benchmarks.md)

EKB-027 为 P3 知识库固定了两类门槛：10 万个 issue 时检索 **p95 小于 150 ms**；真实进程在单次写入、批量写入或迁移途中被终止后，已提交数据仍然完整。150 ms 是验收门槛，不是性能目标。

> 本文记录的是 2026-10-19 对对应源码版本的历史实测，不代表当前 `dist`、已安装 EXE 或旧发布包已经形成一致发布物。当前交付状态见 [project-status.zh-CN.md](project-status.zh-CN.md)。

## 状态

| 门槛 | 结果 |
| --- | --- |
| 指纹精确查找 p95 | 通过（0.6 ms） |
| `pg_trgm` 候选 p95 | 通过（81.7 ms） |
| 完整检索 p95 | 通过（84.5 ms） |
| 崩溃安全（四种杀进程场景） | 通过 |
| 并行真实数据库门禁（WF-013） | 通过，完整全新运行连续 3/3 |
| EKB-027 | **已验收** |

同一生成器与方法的第一次 10 万规模运行中，`pg_trgm` p95 为 155.3 ms，完整检索 p95 为 226.0 ms，因此当时标记为阻塞。查询长度上限和最新出现记录索引修复后，用相同方法复测通过，门槛没有放宽。

## 环境

| 项目 | 值 |
| --- | --- |
| 日期 | 2026-10-19 |
| 系统 | Windows 11 Pro for Workstations 10.0.26200 |
| CPU | Intel Core i9-14900HX（17 核 / 18 逻辑处理器） |
| 内存 | 31.8 GB |
| 磁盘 | KIOXIA EXCERIA G2 SSD |
| Go | go1.26.4 windows/amd64 |
| PostgreSQL | 16.15（EDB，本地实例） |
| `shared_buffers` | 128 MB |
| `work_mem` | 4 MB |
| `random_page_cost` | 4 |
| `max_parallel_workers_per_gather` | 2 |
| `effective_cache_size` | 4 GB |

测试使用 EKB-002 固定的默认本地开发配置，没有额外调优。

## 数据集

`internal/database/bench_test.go` 中的 `seedPerformanceDataset` 使用确定性 SQL 生成数据：

| 属性 | 值 |
| --- | --- |
| Issues | 100,000 |
| Events / Occurrences | 各 221,803 |
| 只有一次出现的问题 | 90,000 |
| 2–19 次的小尾部问题 | 9,900 |
| 50–299 次的重尾问题 | 100 |
| 含中文字符的问题 | 11,111 |
| 分类 | tool 45%、command-exit 35%、llm 15%、agent 5% |
| 项目 | 5 个项目键，每个 20,000 个问题 |

消息由 6–9 个可读伪单词、唯一对象标识和可选中文字符构成。相同错误会因同一指纹合并，所以 10 万个 issue 必须对应 10 万条不同失败文本。事件继承问题标题、项目和指纹；插入后执行 `vacuum (analyze)`，确保测试描述稳定状态，而不是 GIN pending list 的维护时间。确定性测试会比较两次种子生成结果的签名和计数。

## 工作负载与方法

| 类别 | 调用 |
| --- | --- |
| exact | `database.ExactSearch`，按 12 位十六进制指纹 |
| trigram | `database.FuzzyCandidates`，输入问题标题 |
| retrieval | `database.Retrieve`，输入标题和 `tool` 分类 |

- 样本为按指纹排序后的前 64 个 `0%` 问题，是确定性样本而非均匀随机样本。
- 每类预热 10 轮、计量 200 轮；每轮包含数据库调用、行物化和 Go 包装层。
- 百分位使用 nearest-rank；门槛直接测试百分位，而不是只看 `testing.B` 的均值。
- 固定参数：相似度阈值 0.30、候选上限 20、权重 1.00 / 0.40 / 0.20。

## 10 万问题结果

通过的 `TestPerformanceGate` 总计 109.3 秒：

| 类别 | n | p50 | p95 | p99 | p95 < 150 ms |
| --- | --- | --- | --- | --- | --- |
| exact | 200 | 0.0 ms | 0.6 ms | 0.6 ms | 通过 |
| trigram | 200 | 65.5 ms | 81.7 ms | 84.7 ms | 通过 |
| retrieval | 200 | 68.7 ms | 84.5 ms | 87.9 ms | 通过 |

第一次阻塞运行：trigram p50/p95/p99 为 122.1/155.3/158.6 ms；retrieval 为 194.1/226.0/230.2 ms；exact 基本不变。

4,000 个问题的 smoke 结果：exact p95 0.6 ms，trigram p95 9.4 ms，完整检索 p95 12.2 ms。

## 查询计划与修复

`auto_explain` 显示，trigram 候选的主要开销来自 `events_normalized_message_trgm_idx` 的 GIN bitmap scan。查询越长，需要合并的 posting list 越多：100 字符约 74–89 ms，64 字符约 47–58 ms，48 字符约 31–34 ms，32 字符约 20–21 ms。

两项修复均使用相同数据与方法复测：

1. `0007_occurrence_lookup.sql` 增加 `occurrences_issue_latest_idx (issue_id, occurred_at desc, id desc)`。它把最新出现记录的查询从扫描 221,803 行、约 206.8–236.9 ms，降为约 0.025–0.28 ms 的 Index Only Scan。
2. `database.TrigramQueryMaxChars = 64` 只限制送入 `pg_trgm` 候选阶段的 rune 数；Go 重排仍比较完整文本。trigram p95 从 155.3 ms 降到 81.7 ms，标注语料仍保持 12 组近似不同错误零错误自动合并，并保持原有同错召回记录。

尝试过 GiST，但同一函数在 10 万规模下为 444–593 ms，比 GIN 慢约 6–7 倍，因此保留 GIN。

## 崩溃安全

`TestCrashSafety` 启动真实子进程并调用 `Process.Kill`，等待 PostgreSQL 后端从 `pg_stat_activity` 消失后检查数据库：

| 场景 | 断言 |
| --- | --- |
| 单次写入尚未提交时终止 | 计数不变，无残留 advisory lock |
| 单次写入提交后终止 | event、occurrence、issue 各正确存在一次 |
| 无限批量写入约 400 ms 时终止 | `events = occurrences`，无孤儿或半行 |
| 迁移语句执行中终止 | 已提交迁移账本完整，探针表不存在，无残留锁；修改探针后重跑只应用该版本 |

四种场景全部通过。Windows 上服务端不能在长语句中立刻发现客户端死亡，因此迁移探针休眠 8 秒，清理最多等待 45 秒；终止动作仍是真实杀进程。

WF-013 消除了 2026-10-22 发布复核中发现的测试层不确定性。锁断言现在只检查被终止的 `errkb-crash-helper` 后端持有的 advisory lock，不再统计共享数据库中的全部 advisory lock。`TestCrashLockAssertionIgnoresUnrelatedAdvisoryLocks` 会让无关后端持锁，先证明该锁被忽略，再把该后端标识为 crash helper，证明限定后的检查仍能检出目标锁。聚焦的崩溃测试组合连续 3 次通过，随后带真实 DSN 的并行 `go test ./... -count=1` 又连续 3 次全新运行通过；没有使用 `-p 1` 串行化。

## CI 分层与复现

- 设置 `ERRKB_TEST_DATABASE_URL` 时运行 `TestPerformanceSmoke` 和 `TestCrashSafety`。
- 10 万规模 `TestPerformanceGate` 需要额外设置 `ERRKB_PERF_BENCH=1`，用于检索改动验收，不应拖慢普通 CI。
- `ERRKB_PERF_KEEP=1` 会保留生成的 schema 以检查查询计划，之后必须手工删除。
- 当前 Go CI 没有 PostgreSQL 服务，因此数据库 Go 测试会跳过；数据库 job 只跑 SQL 迁移和 50 会话 smoke。这是当前覆盖缺口。

```powershell
$env:ERRKB_TEST_DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'
go test ./internal/database/ -run 'TestPerformanceSmoke|TestCrashSafety|TestPerformanceGenerator' -v -count=1
$env:ERRKB_PERF_BENCH = '1'
$env:ERRKB_PERF_KEEP = '1'
go test ./internal/database/ -run TestPerformanceGate -v -count=1
```

## 限制

- 只有一台机器、一次正式运行；没有方差研究和并发负载，且种子完成后 OS 缓存已热。
- 64 个标题是确定性样本，不是均匀随机样本。
- 结果对应本地 PostgreSQL 默认参数；生产配置可能改变 GIN 成本。
- 相似度阈值固定为 0.30；提高阈值不能消除占主导的 posting-list 扫描。
- 64 rune 上限是已测量的权衡：完整文本仍参与重排，精确和确定性过滤不受影响；但若区分性信息只出现在第 64 rune 之后，它不能单独通过 trigram 阶段生成候选。
