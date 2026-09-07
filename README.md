# BCS Agent

基于 Go + Eino 的 Blueking Container Service 运维 Agent，通过自然语言查询集群与 Kubernetes 资源、读取日志，并在人工确认后执行扩缩容。当前提供终端交互，Web 界面尚未实现。

## 当前能力

- **多轮对话**：流式输出回答，展示工具调用及结果。
- **资源查询**：BCS 项目与集群列表；通过 BCS 网关查询 Kubernetes 原生资源和 CRD，使用 Discovery 解析 Kind/GVR，支持列表、详情和名称片段筛选。
- **排障数据**：查询 Pod、Event 和容器日志，为分析提供依据；稳定的端到端故障诊断场景仍待验证。
- **受控扩缩容**：支持提供 `/scale` 子资源的工作负载；通过 Eino 中断与恢复等待确认，执行前校验资源 UID、resourceVersion 和副本数，拒绝过期变更。

## 快速开始

Go 版本要求见 [go.mod](go.mod)。准备配置后启动：

```bash
cp .env.example .env
# 编辑 .env，填写 OPENAI_API_KEY、OPENAI_MODEL；兼容服务按需填写 OPENAI_BASE_URL
go run ./cmd/bcs-agent cli
```

程序自动读取 `.env`，已设置的环境变量优先。配置说明见 [.env.example](.env.example)。

- **Mock 模式**：未同时配置 `BCS_BASE_URL` 和 `BCS_API_TOKEN` 时使用内置示例数据，仍需调用真实模型服务。
- **真实模式**：同时配置 BCS 地址和 Token，经网关访问目标集群；需具备 Discovery 和目标资源的相应权限。当前默认跳过 BCS TLS 校验，生产环境应设置 `BCS_INSECURE_SKIP_VERIFY=false`。

也可执行一次性提问：

```bash
go run ./cmd/bcs-agent cli -p "查看项目列表"
```

程序使用子命令区分运行入口：`cli` 启动现有终端模式，`server` 预留给 Web 服务。当前 `server` 入口已经建立，HTTP 服务尚未实现。

交互示例（集群和资源名称请替换为实际目标）：

```text
查看集群 BCS-K8S-40888 的 default 命名空间下的 Pod
查看这个集群 default 下 web-0 最近 100 行日志
把这个集群 default 下的 Deployment web 扩到 3 个副本
```

扩缩容会展示变更并等待 `y/N` 确认，一次性提问也不绕过确认；标准输入关闭时不会执行待确认操作。终端支持 `/clear` 清空历史、`/exit` 退出。

## 能力边界

- BCS 项目、集群列表已接入真实 API；BCS 集群详情 API 尚未接入，节点统计通过 Kubernetes 查询完成。真实请求失败不会回退到 Mock。
- 资源默认返回摘要，列表可能分页；单资源可显式请求完整内容，但仍会脱敏，超出大小限制时报错。日志为有限快照，可能裁剪，不支持实时跟随；脱敏不保证覆盖任意业务敏感信息。
- 扩缩容请求被 API 接受不代表 Pod 已就绪，需进一步查询状态。会话与审批检查点仅保存在进程内，重启后丢失。

## 代码结构

调用链：`CLI → chat 会话 → Eino Agent → tools → BCS / Kubernetes 客户端`。

- `cmd/bcs-agent`：启动与组件组装。
- `internal/chat`、`internal/cli`：会话历史、流式事件与终端展示。
- `internal/agent`、`internal/tools/bcs`：Agent 配置、工具错误处理与工具适配。
- `internal/bcs`、`internal/kubernetes`：真实 API 通信、通用资源访问及 Mock。

后续 Web 入口将复用现有会话、Agent 和客户端层。
