package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yuyudeqiu/bcs-agent/internal/agent"
	"github.com/yuyudeqiu/bcs-agent/internal/bcs"
	"github.com/yuyudeqiu/bcs-agent/internal/chat"
	"github.com/yuyudeqiu/bcs-agent/internal/cli"
	"github.com/yuyudeqiu/bcs-agent/internal/config"
	bcstools "github.com/yuyudeqiu/bcs-agent/internal/tools/bcs"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 目前使用内存中的模拟 BCS Client。接入真实 BCS API 时只需替换这里。
	bcsClient := bcs.NewMockClient()
	agentTools := bcstools.NewTools(bcsClient)

	chatAgent, err := agent.New(ctx, cfg.OpenAI, agentTools)
	if err != nil {
		log.Fatalf("初始化 Agent 失败: %v", err)
	}

	session := chat.NewSession(chatAgent)
	if err := cli.Run(ctx, os.Stdin, os.Stdout, session); err != nil {
		fmt.Fprintf(os.Stderr, "BCS Agent 退出: %v\n", err)
		os.Exit(1)
	}
}
