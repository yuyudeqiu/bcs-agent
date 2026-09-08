package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type cliOptions struct {
	prompt  string
	runOnce bool
}

type serverOptions struct {
	addr string
}

type commandRunner struct {
	cli    func(context.Context, cliOptions) error
	server func(context.Context, serverOptions) error
}

func newRootCommand(runner commandRunner, input io.Reader, output, errorOutput io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "bcs-agent",
		Short:         "Blueking Container Service 运维助手",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(errorOutput)
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newCLICommand(runner.cli), newServerCommand(runner.server))
	return root
}

func newCLICommand(run func(context.Context, cliOptions) error) *cobra.Command {
	var prompt string
	var promptSet bool

	command := &cobra.Command{
		Use:   "cli [prompt]",
		Short: "启动终端对话，或执行一次指定的 prompt",
		Args: func(command *cobra.Command, args []string) error {
			promptSet = command.Flags().Changed("prompt")
			if promptSet && len(args) > 0 {
				return fmt.Errorf("不能同时使用 prompt 参数和位置参数")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if !promptSet && len(args) > 0 {
				prompt = strings.Join(args, " ")
			}
			prompt = strings.TrimSpace(prompt)
			if promptSet && prompt == "" {
				return fmt.Errorf("prompt 不能为空")
			}
			return run(command.Context(), cliOptions{
				prompt:  prompt,
				runOnce: promptSet || len(args) > 0,
			})
		},
	}
	command.Flags().StringVarP(&prompt, "prompt", "p", "", "执行一次指定的 prompt 后退出")
	return command
}

func newServerCommand(run func(context.Context, serverOptions) error) *cobra.Command {
	var addr string
	command := &cobra.Command{
		Use:   "server",
		Short: "启动 Web 服务",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(addr) == "" {
				return fmt.Errorf("addr 不能为空")
			}
			return run(command.Context(), serverOptions{addr: addr})
		},
	}
	command.Flags().StringVar(&addr, "addr", ":8080", "HTTP 服务监听地址")
	return command
}
