package web

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"

	"github.com/yuyudeqiu/bcs-agent/internal/chat"
)

func NewDiagnoser(chatAgent adk.Agent) DiagnoseFunc {
	return func(ctx context.Context, request DiagnosisRequest, emit func(DiagnosisEvent) error) error {
		session := chat.NewSession(chatAgent)
		prompt := fmt.Sprintf(`请对用户明确选择的 Kubernetes Pod 做只读故障诊断。
目标：集群 %s，命名空间 %s，Pod %s。
请查询 Pod 状态、相关 Event，并在有必要且能确定容器时查询日志。只根据查询到的证据回答，区分“观察到的证据”“可能原因”“建议下一步”；不要执行扩缩容或其他写操作。`, request.ClusterID, request.Namespace, request.Name)
		err := session.AskStream(ctx, prompt, func(event chat.Event) error {
			switch event.Type {
			case chat.EventTextDelta:
				return emit(DiagnosisEvent{Type: string(event.Type), Content: event.Content})
			case chat.EventToolCall, chat.EventToolResult:
				return emit(DiagnosisEvent{Type: string(event.Type), ToolName: event.ToolName})
			case chat.EventApproval:
				return fmt.Errorf("只读诊断出现了未预期的操作确认")
			default:
				return nil
			}
		})
		if err != nil {
			return err
		}
		if len(session.PendingApprovals()) > 0 {
			return fmt.Errorf("只读诊断出现了未预期的待确认操作")
		}
		return nil
	}
}
