# e2e-test-runner-go

一个自包含的端到端测试运行器：发现仓库中的端到端场景，在相互隔离的环境中
并发执行，并产出机器可读的稳定结果。不依赖任何外部服务，完全本地运行。

## 构建与运行

```sh
go build -o e2e-runner ./cmd/e2e-runner

# 运行 scenarios/ 下的全部场景
./e2e-runner -root scenarios -parallel 4 -output report.json

# 只跑 fast 标签、排除崩溃类场景、打乱顺序（固定种子可复现）
./e2e-runner -root scenarios -include fast -exclude 'crash-*' -shuffle -seed 42
```

仓库自带一组演示场景（`scenarios/`），覆盖通过、失败、卡死、崩溃、抖动、
跳过等情况，可直接用于本地复现验证。

## 场景定义

场景是任意目录下的一个 `scenario.json`：

```json
{
  "name": "flaky-counter",
  "command": "sh run.sh",
  "timeout": "5s",
  "retries": 2,
  "skip": false,
  "tags": ["fast"]
}
```

| 字段      | 说明                                             |
| --------- | ------------------------------------------------ |
| `name`    | 场景名，缺省为相对目录名                         |
| `command` | 必填，通过 `sh -c` 执行                          |
| `timeout` | 单场景超时（如 `"5s"`），缺省用 `-timeout`       |
| `retries` | 失败后重试次数，缺省继承 `-retries`（默认 0）    |
| `skip`    | 为 `true` 时不执行，状态记为 `skipped`           |
| `tags`    | 供 `-include` / `-exclude` 匹配                  |

### 执行环境

- 每次尝试都在**临时目录中的场景副本**里运行，场景之间、重试之间互不污染。
- 命令在**独立进程组**中运行；超时或取消时整组被 `SIGKILL`，不会留下后台
  遗留进程。
- 注入的环境变量：
  - `SCENARIO_NAME`：场景名
  - `SCENARIO_SOURCE_DIR`：场景源目录
  - `SCENARIO_STATE_DIR`：同一场景的多次重试共享、随运行结束清理的状态目录
    （用于表达"偶发失败"类场景）
  - `TMPDIR`：指向本次尝试的临时工作目录

## 命令行参数

| 参数        | 默认值      | 说明                                       |
| ----------- | ----------- | ------------------------------------------ |
| `-root`     | `scenarios` | 场景扫描根目录                             |
| `-include`  | （空）      | 名称/标签 glob，可重复；空表示全部         |
| `-exclude`  | （空）      | 名称/标签 glob，可重复                     |
| `-parallel` | `4`         | 最大并发场景数                             |
| `-retries`  | `0`         | 默认重试次数（场景可自行覆盖）             |
| `-timeout`  | `30s`       | 默认单场景超时                             |
| `-deadline` | `0`（无）   | 整体截止时间，触发后取消仍在运行的场景     |
| `-shuffle`  | `false`     | 随机打乱执行顺序                           |
| `-seed`     | `1`         | 打乱种子；种子相同则顺序一致               |
| `-output`   | （空）      | JSON 报告输出文件，缺省写到标准输出        |

`-shuffle` 只改变执行顺序，不改变参与执行的场景集合；结果报告始终按场景名
排序，与并发度和完成先后无关，同一批场景重复运行结论逐项一致。

## 场景状态

| 状态        | 含义                                       |
| ----------- | ------------------------------------------ |
| `passed`    | 首次尝试即通过                             |
| `flaky`     | 重试后通过（偶发失败），保留完整重试历史   |
| `failed`    | 所有尝试均非零退出（含崩溃）               |
| `timeout`   | 超过单场景超时，进程组已被终止             |
| `cancelled` | 被整体截止时间或信号中断                   |
| `skipped`   | 场景自身声明 `skip: true`                  |
| `excluded`  | 命中 `-exclude`                            |
| `unmatched` | 未命中 `-include`                          |

被过滤的场景不执行，但会带着各自的原因出现在报告中，三者可明确区分。

## 退出码

| 码  | 含义                                                     |
| --- | -------------------------------------------------------- |
| 0   | 无失败/超时/被取消的场景（`flaky` 视为通过）             |
| 1   | 至少一个场景 `failed` 或 `timeout`                       |
| 2   | 运行被中断（`-deadline` 触发或收到 SIGINT/SIGTERM）    |
| 3   | 参数或配置错误                                           |

被中断时（退出码 2）已完成场景的结果仍然保留在报告中，未开始的场景标记为
`cancelled`。

## 报告格式

报告为 JSON（`-output` 指定文件，否则写标准输出），包含汇总与逐场景结果：

```json
{
  "summary": {"total": 6, "by_status": {"passed": 1, ...}, "seed": 1, ...},
  "results": [
    {
      "name": "flaky-counter",
      "status": "flaky",
      "duration_ns": 123456,
      "attempts": [
        {"index": 0, "status": "failed", "exit_code": 1, ...},
        {"index": 1, "status": "passed", "exit_code": 0, ...}
      ]
    }
  ]
}
```

重试只产生 `attempts` 历史，不会重复累计场景结果。

## 测试

```sh
go test ./...          # 全部测试
go test -race ./...    # 含竞态检测
```

测试覆盖：并发度约束、结果顺序与并发无关、超时杀进程组、崩溃隔离、
重试与抖动判定、取消时保留部分结果、打乱种子确定性、过滤状态区分、
退出码语义。
