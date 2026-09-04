package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type options struct {
	prompt  string
	runOnce bool
}

func parseOptions(args []string, output io.Writer) (options, error) {
	flags := flag.NewFlagSet("bcs-agent", flag.ContinueOnError)
	flags.SetOutput(output)
	var prompt string
	flags.StringVar(&prompt, "p", "", "执行一次指定的 prompt 后退出")
	flags.StringVar(&prompt, "prompt", "", "执行一次指定的 prompt 后退出")
	flags.Usage = func() {
		fmt.Fprintln(output, "用法:")
		fmt.Fprintln(output, "  bcs-agent                         进入交互模式")
		fmt.Fprintln(output, "  bcs-agent -p \"查看集群列表\"      执行一次后退出")
		fmt.Fprintln(output, "  bcs-agent 查看集群列表             执行一次后退出")
		fmt.Fprintln(output, "\n参数:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	promptFlagSet := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "p" || current.Name == "prompt" {
			promptFlagSet = true
		}
	})
	positionals := flags.Args()
	if promptFlagSet && len(positionals) > 0 {
		return options{}, fmt.Errorf("不能同时使用 prompt 参数和位置参数")
	}
	if len(positionals) > 0 {
		prompt = strings.Join(positionals, " ")
	}
	prompt = strings.TrimSpace(prompt)
	if promptFlagSet && prompt == "" {
		return options{}, fmt.Errorf("prompt 不能为空")
	}
	return options{prompt: prompt, runOnce: promptFlagSet || len(positionals) > 0}, nil
}
