package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (c *MockClient) Logs(ctx context.Context, q LogsRequest) (LogsResult, error) {
	if err := q.NormalizeAndValidate(); err != nil {
		return LogsResult{}, err
	}
	// 复用现有 Pod fixture，保持集群、命名空间、容器和不存在目标的行为一致。
	result, err := c.Query(ctx, QueryRequest{ClusterID: q.ClusterID, Kind: "Pod", Action: "get", Namespace: q.Namespace, Name: q.Pod, Output: "full"})
	if err != nil {
		return LogsResult{}, err
	}
	var pod corev1.Pod
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(result.Items[0].Resource, &pod); err != nil {
		return LogsResult{}, fmt.Errorf("解析 Mock Pod: %w", err)
	}
	q.Container, err = selectLogContainer(&pod, q.Container)
	if err != nil {
		return LogsResult{}, err
	}
	if q.Previous {
		return LogsResult{}, fmt.Errorf("Mock 示例容器没有上一次运行日志")
	}
	if pod.Status.Phase != corev1.PodRunning {
		return LogsResult{}, fmt.Errorf("Mock 示例容器尚未运行，没有可读取的日志")
	}
	var lines []string
	now := time.Now().UTC()
	for _, entry := range []struct {
		seconds int64
		text    string
	}{{2, "Mock: application started"}, {1, "Mock: listening on :8080"}} {
		if q.SinceSeconds == nil || entry.seconds <= *q.SinceSeconds {
			lines = append(lines, now.Add(-time.Duration(entry.seconds)*time.Second).Format(time.RFC3339Nano)+" "+entry.text+"\n")
		}
	}
	if int64(len(lines)) > *q.TailLines {
		lines = lines[len(lines)-int(*q.TailLines):]
	}
	return formatLogs(q, []byte(strings.Join(lines, "")), "", "mock")
}
