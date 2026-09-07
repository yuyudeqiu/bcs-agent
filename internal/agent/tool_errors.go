package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/compose"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type queryFailure struct {
	OK    bool               `json:"ok"`
	Error queryFailureDetail `json:"error"`
}
type queryFailureDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// 当前工具均为同步 InvokableTool，即使 Agent 启用流式输出，也经过此端点。
// 仅接管已知的只读工具，未来的写操作需要独立定义审批及重试语义。
func recoverQueryErrors(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		output, err := next(ctx, input)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			return output, nil
		}
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		if _, ok := compose.IsInterruptRerunError(err); ok {
			return nil, err
		}
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			return nil, err
		}
		switch input.Name {
		case "list_projects", "list_clusters", "get_cluster_detail", "kubernetes_query", "kubernetes_logs":
		default:
			return nil, err
		}
		failure := queryFailure{OK: false, Error: describeQueryFailure(err)}
		encoded, marshalErr := json.Marshal(failure)
		if marshalErr != nil {
			return nil, marshalErr
		}
		// 丢弃失败调用的任何部分输出，避免把不完整数据与失败状态混在一起。
		return &compose.ToolOutput{Result: string(encoded)}, nil
	}
}

// 写操作错误作为明确的工具失败返回给模型，但不把中断、取消或超时转换为普通结果。
// retryable 始终为 false：任何再次写入都必须重新读取目标并重新取得用户确认。
func recoverScaleErrors(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		output, err := next(ctx, input)
		if err == nil || input.Name != "kubernetes_scale" {
			return output, err
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if _, ok := compose.IsInterruptRerunError(err); ok {
			return nil, err
		}
		if _, ok := compose.ExtractInterruptInfo(err); ok {
			return nil, err
		}
		detail := describeScaleFailure(err)
		encoded, marshalErr := json.Marshal(struct {
			OK        bool               `json:"ok"`
			Retryable bool               `json:"retryable"`
			Error     queryFailureDetail `json:"error"`
		}{OK: false, Retryable: false, Error: detail})
		if marshalErr != nil {
			return nil, marshalErr
		}
		return &compose.ToolOutput{Result: string(encoded)}, nil
	}
}

func describeScaleFailure(err error) queryFailureDetail {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		switch status.Status().Code {
		case 401:
			return queryFailureDetail{"unauthorized", "身份验证失败（HTTP 401）", "操作未确认成功；需要有效凭证。"}
		case 403:
			return queryFailureDetail{"forbidden", "没有扩缩容权限（HTTP 403）", "操作未执行；需要目标资源 scale 子资源的更新权限。"}
		case 404:
			return queryFailureDetail{"not_found", "目标资源或 scale 子资源不存在（HTTP 404）", "操作未执行；核对目标，重新查询后再发起新的确认。"}
		case 409:
			return queryFailureDetail{"conflict", "资源在执行前已变化（HTTP 409）", "不要自动重试；重新查询当前状态，并让用户确认新的变更。"}
		case 429:
			return queryFailureDetail{"rate_limited", "接口请求频率受限（HTTP 429）", "无法确认操作是否完成；先查询当前状态，不直接重复写入。"}
		}
	}
	message := err.Error()
	if strings.Contains(message, "确认期间已变化") {
		return queryFailureDetail{"precondition_failed", message, "操作未执行；重新查询当前状态，并让用户确认新的变更。"}
	}
	return queryFailureDetail{"scale_error", message, "不要自动重试写操作；先查询目标当前状态，再决定是否需要新的用户确认。"}
}

var bcsHTTPError = regexp.MustCompile(`BCS 返回 HTTP ([0-9]{3}):`)
var bcsBusinessError = regexp.MustCompile(`BCS 返回错误 code=(-?[0-9]+)`)

func describeQueryFailure(err error) queryFailureDetail {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return describeHTTPFailure(int(status.Status().Code))
	}
	if match := bcsHTTPError.FindStringSubmatch(err.Error()); match != nil {
		code, _ := strconv.Atoi(match[1])
		return describeHTTPFailure(code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return queryFailureDetail{"timeout", "工具请求超时", "查询未完成；可稍后尝试，避免连续重复请求。"}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return queryFailureDetail{"connection_error", "工具请求连接失败或超时", "检查目标环境是否可达；无法连接时说明限制，不推断资源状态。"}
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) {
		// 客户端的响应解析错误也可能使用这些类型，不能一律称为工具参数错误。
		if strings.Contains(err.Error(), "参数") {
			return queryFailureDetail{"invalid_arguments", "工具参数 JSON 格式或字段类型错误", "根据工具参数声明修正后重新调用。"}
		}
		return queryFailureDetail{"invalid_response", "接口响应解析失败", "不能将解析失败视为空结果；说明接口响应异常。"}
	}
	if match := bcsBusinessError.FindStringSubmatch(err.Error()); match != nil {
		return queryFailureDetail{"business_error", "BCS 业务请求失败，code=" + match[1], "说明业务接口错误，不能据此推断资源不存在。"}
	}
	message := err.Error()
	// 旧客户端可能在解析错误后附带整个响应，不能把这些正文送入模型上下文。
	for _, marker := range []string{" (body=", " (data="} {
		if i := strings.Index(message, marker); i >= 0 {
			message = message[:i]
		}
	}
	runes := []rune(message)
	if len(runes) > 2000 {
		message = string(runes[:2000]) + "…（错误信息已截断）"
	}
	return queryFailureDetail{"query_error", message, "根据错误核对目标和参数；仅在能够修正时重新查询，否则向用户说明限制，不反复原样重试。"}
}

func describeHTTPFailure(code int) queryFailureDetail {
	switch code {
	case 400, 422:
		return queryFailureDetail{"invalid_request", "接口拒绝了请求参数", "核对资源、版本及参数后再查询，不原样重复调用。"}
	case 401:
		return queryFailureDetail{"unauthorized", "身份验证失败（HTTP 401）", "需要有效凭证；向用户说明限制，不反复调用或改查其他集群。"}
	case 403:
		return queryFailureDetail{"forbidden", "没有读取权限（HTTP 403）", "向用户说明权限不足，不通过改查其他目标绕过权限。"}
	case 404:
		return queryFailureDetail{"not_found", "目标资源或接口不存在（HTTP 404）", "核对集群、命名空间、资源名称和 GVR；必要时查询同一范围的列表，不重复使用已失败的相同参数。"}
	case 410:
		return queryFailureDetail{"expired", "查询上下文或分页标记已过期（HTTP 410）", "清除 continue 从第一页重新查询，不合并不同查询快照的结果。"}
	case 429:
		return queryFailureDetail{"rate_limited", "接口请求频率受限（HTTP 429）", "稍后再试，不立即重复调用。"}
	default:
		return queryFailureDetail{"api_error", "接口请求失败（HTTP " + strconv.Itoa(code) + "）", "说明接口异常，无法据此判断资源状态；避免连续重试。"}
	}
}
