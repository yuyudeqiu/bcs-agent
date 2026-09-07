package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"k8s.io/klog/v2"

	"github.com/yuyudeqiu/bcs-agent/internal/agent"
	"github.com/yuyudeqiu/bcs-agent/internal/bcs"
	"github.com/yuyudeqiu/bcs-agent/internal/chat"
	"github.com/yuyudeqiu/bcs-agent/internal/cli"
	"github.com/yuyudeqiu/bcs-agent/internal/config"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
	bcstools "github.com/yuyudeqiu/bcs-agent/internal/tools/bcs"
)

func main() {
	// client-go uses klog for internal diagnostics. Keep errors visible without
	// mixing throttling and discovery INFO messages into the CLI stream.
	klog.SetSlogLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	options, err := parseOptions(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "参数错误: %v\n", err)
		os.Exit(2)
	}

	// 加载 .env（若存在）；已 export 的环境变量优先级更高，不会被覆盖。
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		log.Fatalf("初始化 Agent 失败: %v", err)
	}

	session := chat.NewSession(chatAgent)
	if options.runOnce {
		err = cli.RunOnce(ctx, os.Stdin, os.Stdout, session, options.prompt)
	} else {
		err = cli.Run(ctx, os.Stdin, os.Stdout, session)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "BCS Agent 退出: %v\n", err)
		os.Exit(1)
	}
}
