# BCS Agent

面向 Blueking Container Service 的终端运维 Agent。当前版本提供流式多轮终端对话和 BCS 查询工具（项目列表、集群列表已接入真实 API，集群列表支持可选的项目 ID 过滤；集群详情在真实模式下尚未接入，Mock 模式提供示例数据），模型生成的文本会实时输出。

## 运行

```bash
# 1. 准备环境变量（模板见 .env.example）
cp .env.example .env
#    编辑 .env，填入真实的 OPENAI_API_KEY / OPENAI_MODEL 等

# 2. 启动（程序启动时自动读取 .env；已 export 的环境变量优先级更高，不会被覆盖）
go run ./cmd/bcs-agent
```

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

查询默认使用 `output: "summary"`。需要 CR 的具体配置或同步状态时，Agent 可自行对指定名称执行 `get` 并显式传 `output: "full"`；完整资源对象位于 `items[].resource`，包含 `spec`、`status` 等原有字段。常见凭证字段、敏感环境变量及 last-applied annotation 会脱敏，并标记 `redacted: true`。完整资源超过 64 KiB 时明确报错，不返回截断内容；该模式仅对本次单资源查询生效。

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
