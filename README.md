# BCS Agent

面向 Blueking Container Service 的终端运维 Agent。当前版本提供流式多轮终端对话和 BCS 查询工具（项目列表、集群列表已接入真实 API，集群列表支持可选的项目 ID 过滤，模型生成的文本会实时输出。

## 运行

```bash
# 1. 准备环境变量（模板见 .env.example）
cp .env.example .env
#    编辑 .env，填入真实的 OPENAI_API_KEY / OPENAI_MODEL 等

# 2. 启动（程序启动时自动读取 .env；已 export 的环境变量优先级更高，不会被覆盖）
go run ./cmd/bcs-agent
```

不带参数时进入交互模式。也可以传入一次性 prompt，得到结果后直接退出，适合本地调试和脚本调用：

```bash
go run ./cmd/bcs-agent -p "查看项目列表"
go run ./cmd/bcs-agent --prompt "查看集群 BCS-K8S-10001 的节点数量"
go run ./cmd/bcs-agent 查看项目列表
```

一次性 prompt 仍然使用 `.env` 和流式输出。扩缩容等写操作仍会在终端等待 `y/N` 确认；标准输入已关闭时不会执行待确认操作，并以非零状态退出。

也可以不用 `.env`，直接 export：

```bash
export OPENAI_API_KEY="..."
export OPENAI_MODEL="..."
# 使用兼容 OpenAI API 的服务时可选：
export OPENAI_BASE_URL="..."

# 接入真实 BCS API 时（不设置则使用内置 Mock）；base 填 host：
export BCS_BASE_URL="https://bcs.example.com"
export BCS_API_TOKEN="..."
# BCS dev 证书不自带，默认跳过 TLS 校验；生产设 false 关闭：
# export BCS_INSECURE_SKIP_VERIFY=false

go run ./cmd/bcs-agent
```

查询节点数量可输入“查看某个集群的节点数量”，Agent 会调用 `kubernetes_query`（`action=list`、`kind=Node`），读取所有分页后汇总节点总数、Ready 数和非 Ready 数（含 Unknown 或缺失 Ready 条件）。Ready 不代表可调度，节点总数包含控制平面和工作节点。真实模式复用 BCS 配置，Token 需要有 Discovery 接口及目标集群节点列表的读取权限。未配置 BCS 时返回标记为 `source=mock` 的示例数据。

资源查询可输入“查看某个集群 default 命名空间的 Pod”“查看某个集群的 Namespace”或指定 CRD Kind。`kubernetes_query` 支持 Kubernetes 原生资源和 CRD 的 `list/get`：程序通过 API Discovery 在内部解析目标集群的 preferred GVR，并以 dynamic client 查询；同名 Kind 有歧义或需要固定版本时也可显式传入 GVR。指定资源时使用 `name`，跨命名空间列表需明确 `all_namespaces=true`。列表默认每页 50 条（最多 100），`count` 是本页数量，`has_more` 和 `continue` 表示是否还有下一页。Discovery 映射按集群在内存缓存 10 分钟，真实模式需要 Discovery 接口及目标资源的读取权限。未配置 BCS 时使用标记为 `source=mock` 的示例数据。

只知道名称片段时，例如“查找名称包含 cwlicense 的 Pod”，使用 `list` 和 `name_contains: "cwlicense"`，由客户端筛选后仅返回匹配项。匹配大小写敏感，不支持通配符；完整名称继续用 `get + name`。客户端会跳过无匹配页，找到含匹配项的一页即返回，单次最多扫描 20 页、1000 个资源，并复用原有 15 秒查询超时。`scanned_count` 表示本次扫描数量，`scan_limit_reached` 表示因扫描上限停止；`has_more=true` 表示还有未扫描资源，不保证后续有匹配项，也不能因本次匹配为空就断言目标不存在。继续扫描时保持过滤条件及其他参数不变，原样传回 `continue`；不传 `name_contains` 时保持原有分页行为。

查询默认使用 `output: "summary"`。需要 CR 的具体配置或同步状态时，Agent 可自行对指定名称执行 `get` 并显式传 `output: "full"`；完整资源对象位于 `items[].resource`，包含 `spec`、`status` 等原有字段。常见凭证字段、敏感环境变量及 last-applied annotation 会脱敏，并标记 `redacted: true`。完整资源超过 64 KiB 时明确报错，不返回截断内容；该模式仅对本次单资源查询生效。

日志排障使用 `kubernetes_logs`，例如“查看这个集群 default 下 web-0 的最近 100 行日志”。必填 `cluster_id`、`namespace`、`pod`；单容器可省略 `container`，多容器需明确选择（包括 init 和临时容器）。`tail_lines` 默认 200、范围 1–1000；可用正整数 `since_seconds` 限定最近多少秒，`previous` 默认 false，设为 true 读取上一次容器实例日志。结果始终带时间戳，仅返回一次快照；整个 JSON 输出最多 32 KiB，额外裁剪标记 `truncated=true`，不代表全部历史，也不支持 follow 或日志分页。已知网关 Token、常见凭证模式和 PEM 私钥会脱敏并标记 `redacted`，不保证识别任意业务敏感内容。真实模式通过 BCS 网关访问 Pod 和 `pods/log`，需要两者的读取权限；Mock 提供 `default/web-0` 的示例日志，不模拟历史日志。

扩缩容使用 `kubernetes_scale`，例如“把这个集群 default 下的 StatefulSet db 扩到 3 个副本”。工具接受明确的 `cluster_id`、`namespace`、`name`、目标 `replicas`，通常只需再传 Kind；Kind 有歧义或需要固定版本时可显式传完整 GVR。程序通过 Discovery 确认资源提供 `/scale` 子资源，因此不仅支持 Deployment 和 StatefulSet，也支持实际配置了 scale 子资源的 CRD。执行前会展示当前副本数和目标副本数并在终端等待确认；确认与资源 UID、`resourceVersion` 和当前副本数绑定，目标在确认期间变化时拒绝执行，失败后不会自动重复写入。`submitted=true` 只表示 API 已接受更新；`converged` 只比较 Scale 返回的 observed replicas，不表示 Pod 已 Ready。真实模式需要目标主资源的读取权限以及其 scale 子资源的读取和更新权限。

终端命令：

- `/clear`：清空当前对话历史
- `/exit`：退出程序

## 目录

- `cmd/bcs-agent`：程序入口
- `internal/agent`：模型和 Agent 配置
- `internal/chat`：会话和多轮历史
- `internal/cli`：终端交互入口
- `internal/tools/bcs`：提供给模型调用的 BCS 工具
- `internal/bcs`：BCS Client 接口、数据类型、Mock 与真实 HTTP 实现
- `internal/kubernetes`：client-go 网关客户端、资源查询与摘要

后续接入 Web 时，Web Handler 将复用 `internal/chat`、`internal/agent` 和工具层。
