package main

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"

	"github.com/yuyudeqiu/bcs-agent/internal/agent"
	"github.com/yuyudeqiu/bcs-agent/internal/bcs"
	"github.com/yuyudeqiu/bcs-agent/internal/chat"
	"github.com/yuyudeqiu/bcs-agent/internal/cli"
	"github.com/yuyudeqiu/bcs-agent/internal/config"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
	bcstools "github.com/yuyudeqiu/bcs-agent/internal/tools/bcs"
)

func runCLI(ctx context.Context, options cliOptions) error {
	// 加载 .env（若存在）；已 export 的环境变量优先级更高，不会被覆盖。
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// 未配置 BCS_BASE_URL / BCS_API_TOKEN 时使用内置 Mock，配置后走真实 API。
	bcsClient := bcs.Client(bcs.NewMockClient())
	kubernetesClient := kubeclient.Client(kubeclient.NewMockClient())
	if cfg.BCS.BaseURL != "" && cfg.BCS.APIToken != "" {
		bcsClient = bcs.NewHTTPClient(cfg.BCS)
		kubernetesClient = kubeclient.NewGatewayClient(cfg.BCS)
	}
	agentTools := bcstools.NewTools(bcsClient, kubernetesClient)

	chatAgent, err := agent.New(ctx, cfg.OpenAI, agentTools)
	if err != nil {
		return fmt.Errorf("初始化 Agent 失败: %w", err)
	}

	session := chat.NewSession(chatAgent)
	if options.runOnce {
		return cli.RunOnce(ctx, os.Stdin, os.Stdout, session, options.prompt)
	}
	return cli.Run(ctx, os.Stdin, os.Stdout, session)
}
