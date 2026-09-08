package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/joho/godotenv"

	"github.com/yuyudeqiu/bcs-agent/internal/agent"
	"github.com/yuyudeqiu/bcs-agent/internal/bcs"
	"github.com/yuyudeqiu/bcs-agent/internal/config"
	kubeclient "github.com/yuyudeqiu/bcs-agent/internal/kubernetes"
	bcstools "github.com/yuyudeqiu/bcs-agent/internal/tools/bcs"
	webui "github.com/yuyudeqiu/bcs-agent/internal/web"
)

func runServer(ctx context.Context, options serverOptions) error {
	// Web 基础功能不依赖模型配置，只读取 BCS 相关环境变量。
	_ = godotenv.Load()
	bcsConfig := config.LoadBCS()
	bcsClient := bcs.Client(bcs.NewMockClient())
	kubernetesClient := kubeclient.Client(kubeclient.NewMockClient())
	if bcsConfig.BaseURL != "" && bcsConfig.APIToken != "" {
		bcsClient = bcs.NewHTTPClient(bcsConfig)
		kubernetesClient = kubeclient.NewGatewayClient(bcsConfig)
	}

	var diagnose webui.DiagnoseFunc
	openAIConfig, configErr := config.LoadOpenAI()
	if configErr != nil {
		slog.Warn("Agent 分析功能不可用", "error", configErr)
	} else {
		chatAgent, err := agent.New(ctx, openAIConfig, bcstools.NewReadOnlyTools(bcsClient, kubernetesClient))
		if err != nil {
			slog.Error("初始化 Web Agent 失败", "error", err)
		} else {
			diagnose = webui.NewDiagnoser(chatAgent)
		}
	}

	server := &http.Server{
		Addr:              options.addr,
		Handler:           webui.NewHandler(webui.Dependencies{BCS: bcsClient, Kubernetes: kubernetesClient, Diagnose: diagnose}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", options.addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", options.addr, err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	slog.Info("BCS Agent Web Server 已启动", "addr", listener.Addr().String())

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("启动 Web Server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("关闭 Web Server: %w", err)
		}
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("关闭 Web Server: %w", err)
		}
		return nil
	}
}
