# 测试计划制定流程

本文用于把一个测试需求转成可执行的 wkbench 测试方案。

核心原则：优先通过 `YAML 场景 + unit 组合` 完成测试。只有现有 unit 无法表达需求时，才新增 unit。

## 1. 明确测试需求

先把需求拆成四类信息：

- 目标：要验证什么行为或性能指标。
- 对象：目标服务、协议、接口、用户、群、连接或消息类型。
- 负载：QPS、并发、持续时间、消息大小、用户数、群数等。
- 通过标准：错误率、p95/p99、成功数、资源指标或断言条件。

不要先写代码。先确认测试能否用已有能力表达。

## 2. 盘点现有 unit

先查已有 unit：

```bash
GOWORK=off go run ./cmd/wkbench list-units
```

按能力匹配：

- 目标描述：`wukongim.target`
- 用户/身份：`identity.pool`
- 单聊目标：`identity.person_pairs`
- 群准备：`wukongim.prepare_group_channels`
- token 准备：`wukongim.prepare_tokens`
- 会话连接：`wkproto.session_pool`
- 发送压测：`traffic.send`、`traffic.group_send`
- 指标采集：`wukongim.metrics_collector`
- 结果断言：`report.assert`

如果 unit 的输入/输出 port 能串起来，优先写 YAML。

## 3. 判断是否需要新增 unit

只有出现以下情况才新增 unit：

- 现有 unit 没有目标能力。
- 现有 port 无法表达必要输入或输出。
- 需要新的协议动作、准备动作、采集动作或断言能力。
- 测试逻辑不能合理放进 YAML 参数。

新增 unit 时遵守：

- unit 只做一个清晰职责。
- unit 不导入其他 `units/*` 包。
- 输入输出通过 `benchkit/ports/*` 表达。
- `Validate` 只校验本地 spec，不访问目标服务。
- 行为通过 unit 自身测试覆盖。

## 4. 组合 YAML 场景

用场景 YAML 编排测试：

- 用 `inputs` 连接 unit 输出。
- 用 `after` 表达执行顺序。
- 多个 unit 可提供同类 port 时，必须显式写 `inputs`。
- 背景采集类 unit 应先启动，前景 workload 通过 `after: [metrics]` 等待它。
- 报告目录写在 `run.report_dir`，不要把原始大样本写入 `summary.md`。

先从最小可运行场景开始，再扩大参数。

## 5. 验证场景图

写完 YAML 后先验证，不要直接跑真实目标：

```bash
GOWORK=off go run ./cmd/wkbench validate -scenario ./path/to/scenario.yaml
GOWORK=off go run ./cmd/wkbench explain -scenario ./path/to/scenario.yaml
GOWORK=off go run ./cmd/wkbench plan -scenario ./path/to/scenario.yaml
```

检查：

- unit 顺序是否正确。
- inputs 是否连接到预期 port。
- 背景 unit 是否在 workload 前启动。
- 参数是否符合测试需求。

## 6. 执行测试

建议按顺序推进：

1. dry-run 或 fake unit 测试。
2. 小规模真实 smoke。
3. 目标规模压测。
4. 多轮或 sweep 测试。

真实测试必须保留报告目录，便于复查 `report.json`、`summary.md` 和 artifacts。

## 7. 分析结果

优先读取结构化报告：

- `report.json`：机器可读结果。
- `summary.md`：人工快速查看。
- `artifacts/*`：原始样本、采集数据或调试文件。

关注：

- 实际 QPS，而不是只看 offered QPS。
- p95/p99 是否以毫秒展示。
- 错误率和失败 unit。
- 背景指标采集是否完整。
- 断言 unit 是否覆盖通过标准。

## 8. 输出测试计划

测试计划应包含：

- 测试目标。
- 使用的 unit 列表。
- 是否需要新增 unit；如需要，说明职责和 port。
- YAML 场景路径。
- 运行命令。
- 通过标准。
- 报告和 artifacts 检查点。

## 快速决策

```text
测试需求
  -> 查现有 unit
    -> 能表达：写 YAML 场景
    -> 不能表达：新增最小 unit，再写 YAML 场景
  -> validate / explain / plan
  -> 小规模 smoke
  -> 目标规模测试
  -> 用 report.json / summary.md / artifacts 验收
```
