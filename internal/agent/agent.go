package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/yuyudeqiu/bcs-agent/internal/config"
)

func New(ctx context.Context, cfg config.OpenAIConfig, tools []tool.BaseTool) (adk.Agent, error) {
	reasoningEffort := openai.ReasoningEffortLevelHigh
	if cfg.ReasoningEffort != "" {
		reasoningEffort = openai.ReasoningEffortLevel(cfg.ReasoningEffort)
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:          cfg.APIKey,
		Model:           cfg.Model,
		BaseURL:         cfg.BaseURL,
		ByAzure:         cfg.ByAzure,
		ReasoningEffort: reasoningEffort,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModel: %w", err)
	}

	return newWithModel(ctx, chatModel, tools)
}

func newWithModel(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.BaseTool) (adk.Agent, error) {
	chatAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "bcs_agent",
		Description: "Blueking Container Service 运维助手",
		Instruction: systemPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: recoverQueryErrors}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModelAgent: %w", err)
	}

	return chatAgent, nil
}
