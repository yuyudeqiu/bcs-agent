package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yuyudeqiu/bcs-agent/internal/chat"
)

var errApprovalInputClosed = errors.New("操作等待确认，但标准输入已关闭")

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

		err := runQuestion(ctx, scanner, output, session, question)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			fmt.Fprintf(output, "错误> %v\n", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取终端输入: %w", err)
	}
	return nil
}

// RunOnce executes one prompt and exits. It still reads approval decisions from input.
func RunOnce(ctx context.Context, input io.Reader, output io.Writer, session *chat.Session, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("prompt 不能为空")
	}
	if err := runQuestion(ctx, bufio.NewScanner(input), output, session, prompt); err != nil {
		return err
	}
	return nil
}

func runQuestion(ctx context.Context, scanner *bufio.Scanner, output io.Writer, session *chat.Session, question string) error {
	answerLineOpen := false
	emit := func(event chat.Event) error {
		switch event.Type {
		case chat.EventTextDelta:
			if !answerLineOpen {
				fmt.Fprint(output, "Agent> ")
				answerLineOpen = true
			}
			fmt.Fprint(output, event.Content)
		case chat.EventToolCall:
			closeAnswerLine(output, &answerLineOpen)
			fmt.Fprintf(output, "[调用工具] %s(%s)\n", event.ToolName, event.Arguments)
		case chat.EventToolResult:
			closeAnswerLine(output, &answerLineOpen)
			fmt.Fprintf(output, "[工具结果] %s: %s\n", event.ToolName, event.Content)
		case chat.EventApproval:
			closeAnswerLine(output, &answerLineOpen)
			fmt.Fprintf(output, "[需要确认] %s\n", formatApproval(event.Content))
		}
		return nil
	}

	if err := session.AskStream(ctx, question, emit); err != nil {
		closeAnswerLine(output, &answerLineOpen)
		return err
	}
	closeAnswerLine(output, &answerLineOpen)

	for len(session.PendingApprovals()) > 0 {
		approvals := session.PendingApprovals()
		decisions := make(map[string]bool, len(approvals))
		for _, approval := range approvals {
			for {
				fmt.Fprint(output, "确认执行此操作？[y/N] ")
				if !scanner.Scan() {
					if err := scanner.Err(); err != nil {
						return fmt.Errorf("读取确认输入: %w", err)
					}
					return errApprovalInputClosed
				}
				answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
				switch answer {
				case "y", "yes", "是", "确认":
					decisions[approval.ID] = true
				case "", "n", "no", "否", "取消":
					decisions[approval.ID] = false
				default:
					fmt.Fprintln(output, "请输入 y/yes 确认，或 n/no 取消。")
					continue
				}
				break
			}
		}
		if err := session.ResumeApprovals(ctx, decisions, emit); err != nil {
			closeAnswerLine(output, &answerLineOpen)
			return err
		}
		closeAnswerLine(output, &answerLineOpen)
	}
	return nil
}

func closeAnswerLine(output io.Writer, open *bool) {
	if *open {
		fmt.Fprintln(output)
		*open = false
	}
}

func formatApproval(content string) string {
	var plan struct {
		ClusterID       string `json:"cluster_id"`
		Kind            string `json:"kind"`
		Namespace       string `json:"namespace"`
		Name            string `json:"name"`
		CurrentReplicas int32  `json:"current_replicas"`
		TargetReplicas  int32  `json:"target_replicas"`
	}
	if err := json.Unmarshal([]byte(content), &plan); err != nil || plan.ClusterID == "" || plan.Kind == "" || plan.Namespace == "" || plan.Name == "" {
		return content
	}
	return fmt.Sprintf("集群 %s 的 %s %s/%s：副本数 %d → %d", plan.ClusterID, plan.Kind, plan.Namespace, plan.Name, plan.CurrentReplicas, plan.TargetReplicas)
}
