# BCS Agent

面向 Blueking Container Service 的终端运维 Agent。当前版本提供流式多轮终端对话和 BCS 查询工具（项目列表已接入真实 API，集群查询仍为 Mock），模型生成的文本会实时输出。

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

后续接入 Web 时，Web Handler 将复用 `internal/chat`、`internal/agent` 和工具层。
