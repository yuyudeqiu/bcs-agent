# BCS Agent

面向 Blueking Container Service 的终端运维 Agent。当前版本提供流式多轮终端对话和模拟的 BCS 集群查询工具，模型生成的文本会实时输出。

## 运行

```bash
export OPENAI_API_KEY="..."
export OPENAI_MODEL="..."
# 使用兼容 OpenAI API 的服务时可选：
export OPENAI_BASE_URL="..."

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
- `internal/bcs`：BCS Client 接口、数据类型和当前的模拟实现

后续接入 Web 时，Web Handler 将复用 `internal/chat`、`internal/agent` 和工具层。
