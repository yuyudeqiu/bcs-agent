package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yuyudeqiu/bcs-agent/internal/chat"
)

func Run(ctx context.Context, input io.Reader, output io.Writer, session *chat.Session) error {
	fmt.Fprintln(output, "BCS Agent 已启动。输入 /help 查看命令，输入 /exit 退出。")

	scanner := bufio.NewScanner(input)
	for {
		fmt.Fprint(output, "\n你> ")
		if !scanner.Scan() {
			break
		}

		question := strings.TrimSpace(scanner.Text())
		if question == "" {
			continue
		}

		switch question {
		case "/exit", "/quit":
			fmt.Fprintln(output, "再见。")
			return nil
		case "/clear":
			session.Reset()
			fmt.Fprintln(output, "对话历史已清空。")
			continue
		case "/help":
			fmt.Fprintln(output, "/clear  清空当前对话历史")
			fmt.Fprintln(output, "/exit   退出程序")
			continue
		}

		answerLineOpen := false
		err := session.AskStream(ctx, question, func(event chat.Event) error {
			switch event.Type {
			case chat.EventTextDelta:
				if !answerLineOpen {
					fmt.Fprint(output, "Agent> ")
					answerLineOpen = true
				}
				fmt.Fprint(output, event.Content)
			case chat.EventToolCall:
				if answerLineOpen {
					fmt.Fprintln(output)
					answerLineOpen = false
				}
				fmt.Fprintf(output, "[调用工具] %s(%s)\n", event.ToolName, event.Arguments)
			case chat.EventToolResult:
				if answerLineOpen {
					fmt.Fprintln(output)
					answerLineOpen = false
				}
				fmt.Fprintf(output, "[工具结果] %s: %s\n", event.ToolName, event.Content)
			}
			return nil
		})
		if answerLineOpen {
			fmt.Fprintln(output)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			fmt.Fprintf(output, "错误> %v\n", err)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取终端输入: %w", err)
	}
	return nil
}
