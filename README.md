# e2e-test-runner-go

一个不依赖任何外部服务的端到端测试运行器。它从目录树中发现测试场景，把每个场景
放进**独立进程组**隔离执行，支持并发、超时/截止、失败重试与抖动判定、随机打乱
和条件筛选，最终输出一份**与并发度和完成顺序无关、可重复复现**的机器可读结果。

## 快速开始

```bash
# 构建
go build -o e2e-runner ./cmd/e2e-runner

# 运行内置演示场景（testdata/e2e，纯 POSIX shell，无需网络/外部服务）
./e2e-runner -root testdata/e2e -concurrency 4

# 只跑带 smoke 标签的场景，结果同时写入文件
./e2e-runner -tag smoke -report out/report.json

# 固定种子打乱顺序；整体 30s 截止
./e2e-runner -shuffle -seed 42 -deadline 30s -concurrency 8
```

运行自动化测试：

```bash
go test ./...              # 全量
go test -race ./...        # 含竞态检测
go test -v ./internal/runner/   # 并发/取消/重试/隔离用例
```

## 场景（scenario）模型

一个场景就是一个包含 `scenario.json` 的目录，命令以该目录为工作目录执行：

```json
{
  "name": "checkout-flow",            // 必填，全仓库唯一
  "description": "端到端下单流程",
  "command": ["./run.sh", "--arg"],   // 必填，非空数组；含 / 时按场景目录解析
  "timeout": "5s",                    // 单场景超时（Go duration，留空=不限）
  "retries": 2,                       // 初始尝试之外的“额外”重试次数
  "idempotent": true,                 // 是否可安全重复执行，见重试语义
  "tags": ["smoke", "billing"],
  "skip": false,
  "skip_reason": "E2E-42 跟踪中"       // skip=true 时必填
}
```

发现规则：递归扫描 `-root` 下所有名为 `scenario.json` 的文件，按路径排序，
该顺序就是结果中的稳定顺序。重名、JSON 非法、字段未知或约束不满足都会被拒绝。

场景运行时还会拿到以下环境变量：`E2E_RUNNER_SCENARIO`（名称）、
`E2E_RUNNER_ATTEMPT`（从零开始的尝试序号，可用于模拟抖动）。

## 命令行参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-root` | `testdata/e2e` | 递归发现 `scenario.json` 的根目录 |
| `-concurrency` | `1` | 同时运行的场景数，必须 ≥ 1 |
| `-deadline` | `0` | 整轮运行的总截止时间（如 `30s`），`0` 表示不限 |
| `-shuffle` | `false` | 执行前随机打乱**入选**场景顺序 |
| `-seed` | `0` | 打乱用随机种子；同种子顺序一致，不同种子只改顺序不改集合 |
| `-name` | 无 | 按精确名称纳入，可重复或逗号分隔 |
| `-tag` | 无 | 纳入携带任一指定标签的场景，可重复或逗号分隔 |
| `-exclude` | 无 | 按名称强制排除，可重复或逗号分隔 |
| `-report` | 无 | 除 stdout 外，把 JSON 报告原子写入该路径 |
| `-format` | `json` | stdout 输出格式：`json` 或 `text` |

筛选的判定优先级（先命中先生效）：**excluded（排除） > skipped（清单跳过）
> unmatched（未匹配） > 执行**。三种“不执行”原因在结果里分别以
`excluded` / `skipped` / `unmatched` 状态呈现，且每条都带 `reason`。

## 隔离与资源回收

每次尝试（attempt）都拥有：

- **独立进程组**（`setpgid`）：超时或取消时对整组发 `SIGKILL`，命令 fork 出的
  后代（死循环 shell、孙进程等）一起被回收；配合 `Pdeathsig` 兜底，
  **不会留下后台遗留进程**；
- 独立临时 `HOME` / `TMPDIR`，结束即删除，不污染其它场景；
- 清洗过的环境变量（默认只透传 `PATH` 与 `E2E_` 前缀的自定义变量）；
- 场景目录作为工作目录；stdout/stderr 截断保留最后 4KiB 便于解释失败。

单个场景崩溃（被信号杀死）、非零退出、死循环都被封装成该场景自己的结果，
worker 继续处理后续场景，互不影响。

## 超时、截止与中断

- **单场景超时**：来自清单 `timeout`，命中后状态为 `timeout`，写明原因。
- **整体截止**：`-deadline` 到期或收到 `SIGINT` / `SIGTERM` 时，仍在运行的
  场景被强杀并标记为 `canceled`（若先撞上场景自身超时则为 `timeout`）；
  尚未开始的场景标记为 `not_executed`。已完成场景的结果**照常保留**，
  并在中断时仍写入 `-report` 文件（临时文件 + rename 原子落盘）。

## 重试与抖动（flaky）语义

- 初次尝试通过：`passed`。
- 失败后重试、最终通过：`flaky`，`retries` 记录使用的重试次数，
  `attempts` 保留完整历史（每次的状态、退出码、信号、耗时、原因、输出尾部）。
- 重试用尽仍失败：`failed` 或 `crashed`，**结果只计一次**，不会因重试产生多行。
- `timeout` / `canceled` 属于中断，**绝不重试**，避免把被杀死的场景再跑一遍。
- `idempotent:false`（默认）的场景即使配置了 `retries` 也**只执行一次**，
  结果原因中注明 “retries suppressed for non-idempotent scenario”，
  保证有副作用的场景不会被重复执行。
- `idempotent:true` 才允许真正的重试。

## 并发与确定性

- 固定大小的 worker 池执行场景；场景之间不共享可变状态。
- 无论并发度、完成先后如何，结果**始终按发现顺序排列**。
- `-shuffle` 只改变入选场景的*执行*顺序（基于种子的 Fisher-Yates，
  `math/rand/v2` ChaCha8）：同种子顺序一致；换种子不会改变参与执行的集合。

## 结果 JSON

顶层结构：

```json
{
  "started_at": "2026-10-05T...",
  "ended_at":   "2026-10-05T...",
  "deadline_exceeded": false,
  "canceled": false,
  "results": [
    {
      "name": "flaky-pass",
      "dir": "testdata/e2e/05-flaky",
      "tags": [],
      "status": "flaky",
      "reason": "passed on attempt 2 after 1 failed attempt(s)",
      "duration_ms": 21,
      "retries": 1,
      "attempts": [
        {"index": 0, "status": "failed", "exit_code": 1, "duration_ms": 5,
         "reason": "exit status 1", "signal": "", "output_tail": "...",
         "started_at": "..."},
        {"index": 1, "status": "passed", "exit_code": 0, "duration_ms": 4,
         "started_at": "..."}
      ]
    }
  ]
}
```

场景状态取值：

| 状态 | 含义 |
| --- | --- |
| `passed` | 初次尝试即通过 |
| `flaky` | 失败后重试才通过（结果仍按成功计，但可被识别为偶发） |
| `failed` | 非零退出，重试用尽（或未配置重试） |
| `crashed` | 被信号终止或命令无法启动 |
| `timeout` | 单场景超时被强杀 |
| `canceled` | 整体截止/取消时仍在运行，被中断 |
| `skipped` | 清单中 `skip:true` |
| `excluded` | 被 `-exclude` 强制排除 |
| `unmatched` | 不满足 `-name`/`-tag` 筛选 |
| `not_executed` | 运行被中断，排队中从未开始 |

## 退出码

| 码 | 语义 |
| --- | --- |
| `0` | 所有执行的场景通过（`flaky` 算成功；跳过/排除/未匹配不影响） |
| `1` | 至少一个场景 `failed` / `crashed` / `timeout` |
| `2` | 运行被中断（`-deadline` 到期或收到中断信号） |
| `3` | 用法/配置/发现错误（非法 flag、根目录不存在、清单非法等） |

中断（2）优先于单场景失败（1），避免把“没跑完”的一轮误读成“完整但失败”。

## 内置演示场景

`testdata/e2e/` 下有 11 个纯 shell 场景，覆盖正常通过、固定失败、自杀崩溃、
死循环超时、重试后通过、重试仍失败、清单跳过、非幂等抑制重试、慢场景、
标签筛选、fork 孙进程验证进程组回收。直接运行即可在本地复现全部行为。

## 包结构

```
cmd/e2e-runner/        CLI：flag、信号、原子写报告、退出码
internal/scenario/     类型定义、清单加载与确定性发现
internal/filter/       included / excluded / skipped / unmatched 判定
internal/shuffle/      定种子 Fisher-Yates 洗牌
internal/executor/     单尝试：进程组、环境隔离、超时/取消回收
internal/runner/       发现→筛选→洗牌→并发调度→重试聚合
internal/report/       JSON/文本输出、计数、退出码映射
testdata/e2e/          无外部依赖的演示场景
```
