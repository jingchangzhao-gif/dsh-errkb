# 工作流控制中心

[English](README.md)

本目录是项目修复和清理工作的唯一恢复入口，集中保存活动问题队列、清理判断依据以及后续修复的准确文件顺序。

## 阅读顺序

1. [当前工作流与未解决问题](current-workflow.zh-CN.md)——活动问题队列，从唯一的 `NEXT` 行开始。
2. [最新验收报告](acceptance-report-2026-10-22.zh-CN.md)——哪些门禁通过、哪些失败以及禁止发布的原因。
3. [文件修复顺序](file-repair-sequence.zh-CN.md)——每个工作流编号对应的文件、修改、测试和完成门槛。
4. [无用代码审计](unused-code-audit.zh-CN.md)——哪些内容可删除、已过期、未接入、只用于兼容或仍在使用。
5. [项目当前状态](../project-status.zh-CN.md)——已经由 WF-014 同步的当前交付事实和发布边界。
6. [项目实施计划](../implementation-plan.zh-CN.md)——产品决定和历史 EKB 任务顺序。

## 文档职责

- `current-workflow.zh-CN.md` 决定**还剩什么、下一步做什么**。
- `file-repair-sequence.zh-CN.md` 决定**修改哪些文件以及修改顺序**。
- `unused-code-audit.zh-CN.md` 决定**什么可以删除以及删除条件**。
- `project-status.zh-CN.md` 继续决定**当前什么可用、什么可发布**。
- `implementation-plan.zh-CN.md` 保存产品决定、已完成任务证据和验收标准。

不要在其他位置再创建活动待办清单。问题解决后，把长期证据写入对应状态、计划、验收或 CHANGELOG 文件，再从中英文 current-workflow 活动表删除该行。

## 目录内容

| 中文 | English | 用途 |
| --- | --- | --- |
| `README.zh-CN.md` | `README.md` | 文件夹索引与文档职责 |
| `current-workflow.zh-CN.md` | `current-workflow.md` | 只保存按顺序排列的未解决问题 |
| `acceptance-report-2026-10-22.zh-CN.md` | `acceptance-report-2026-10-22.md` | 本轮验收证据与决定 |
| `file-repair-sequence.zh-CN.md` | `file-repair-sequence.md` | 每项问题的文件顺序和验证方法 |
| `unused-code-audit.zh-CN.md` | `unused-code-audit.md` | 清理分类与删除门槛 |
