package kubernetes

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// 摘要只选取排障字段，不返回完整 spec、环境变量、annotations 或 managedFields。
func summarizeResource(item unstructured.Unstructured, token string) (ResourceSummary, error) {
	result := ResourceSummary{APIVersion: item.GetAPIVersion(), Kind: item.GetKind(), Name: item.GetName(), Namespace: item.GetNamespace(), Details: map[string]any{}}
	d := result.Details
	convert := func(out any) error { return runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, out) }
	switch item.GetKind() {
	case "Pod":
		var pod corev1.Pod
		if err := convert(&pod); err != nil {
			return ResourceSummary{}, err
		}
		d["phase"] = pod.Status.Phase
		d["node_name"] = pod.Spec.NodeName
		d["reason"] = cleanQueryText(pod.Status.Reason, token)
		ready, restarts := 0, int32(0)
		containers := []map[string]any{}
		add := func(statuses []corev1.ContainerStatus, init bool) {
			for _, s := range statuses {
				if !init {
					if s.Ready {
						ready++
					}
					restarts += s.RestartCount
				}
				entry := map[string]any{"name": s.Name, "init": init, "ready": s.Ready, "restart_count": s.RestartCount}
				switch {
				case s.State.Waiting != nil:
					entry["state"] = "waiting"
					entry["reason"] = cleanQueryText(s.State.Waiting.Reason, token)
				case s.State.Terminated != nil:
					entry["state"] = "terminated"
					entry["reason"] = cleanQueryText(s.State.Terminated.Reason, token)
					entry["exit_code"] = s.State.Terminated.ExitCode
				case s.State.Running != nil:
					entry["state"] = "running"
				default:
					entry["state"] = "unknown"
				}
				if s.LastTerminationState.Terminated != nil {
					entry["last_termination_reason"] = cleanQueryText(s.LastTerminationState.Terminated.Reason, token)
				}
				containers = append(containers, entry)
			}
		}
		add(pod.Status.InitContainerStatuses, true)
		add(pod.Status.ContainerStatuses, false)
		d["containers"] = containers
		d["ready_containers"] = ready
		d["total_containers"] = len(pod.Spec.Containers)
		d["restart_count"] = restarts
		conditions := []map[string]any{}
		for _, cond := range pod.Status.Conditions {
			conditions = append(conditions, map[string]any{"type": cond.Type, "status": cond.Status, "reason": cleanQueryText(cond.Reason, token)})
		}
		d["conditions"] = conditions
	case "Deployment":
		var deployment appsv1.Deployment
		if err := convert(&deployment); err != nil {
			return ResourceSummary{}, err
		}
		if deployment.Spec.Replicas != nil {
			d["desired_replicas"] = *deployment.Spec.Replicas
		}
		d["ready_replicas"] = deployment.Status.ReadyReplicas
		d["available_replicas"] = deployment.Status.AvailableReplicas
		d["updated_replicas"] = deployment.Status.UpdatedReplicas
		d["generation"] = deployment.Generation
		d["observed_generation"] = deployment.Status.ObservedGeneration
		conditions := []map[string]any{}
		for _, cond := range deployment.Status.Conditions {
			conditions = append(conditions, map[string]any{"type": cond.Type, "status": cond.Status, "reason": cleanQueryText(cond.Reason, token)})
		}
		d["conditions"] = conditions
	case "Namespace":
		var namespace corev1.Namespace
		if err := convert(&namespace); err != nil {
			return ResourceSummary{}, err
		}
		d["phase"] = namespace.Status.Phase
	case "Node":
		var node corev1.Node
		if err := convert(&node); err != nil {
			return ResourceSummary{}, err
		}
		d["kubelet_version"] = node.Status.NodeInfo.KubeletVersion
		d["unschedulable"] = node.Spec.Unschedulable
		d["ready"] = "Unknown"
		conditions := []map[string]any{}
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				d["ready"] = cond.Status
			}
			conditions = append(conditions, map[string]any{"type": cond.Type, "status": cond.Status, "reason": cleanQueryText(cond.Reason, token)})
		}
		d["conditions"] = conditions
	case "Event":
		var event corev1.Event
		if err := convert(&event); err != nil {
			return ResourceSummary{}, err
		}
		d["type"] = event.Type
		d["reason"] = cleanQueryText(event.Reason, token)
		d["message"] = cleanQueryText(event.Message, token)
		d["count"] = event.Count
		d["first_timestamp"] = event.FirstTimestamp
		d["last_timestamp"] = event.LastTimestamp
		d["event_time"] = event.EventTime
		if event.Series != nil {
			d["series_count"] = event.Series.Count
			d["last_observed_time"] = event.Series.LastObservedTime
		}
		d["involved_object"] = map[string]any{"kind": event.InvolvedObject.Kind, "namespace": event.InvolvedObject.Namespace, "name": event.InvolvedObject.Name}
	default:
		// CRD 没有编译期 Go 类型，只返回常见状态标量和条件，避免把完整 spec/status 塞入模型上下文。
		if status, ok := item.Object["status"].(map[string]any); ok {
			for _, key := range []string{"phase", "state", "ready", "replicas", "readyReplicas", "availableReplicas", "updatedReplicas", "observedGeneration"} {
				if value, exists := status[key]; exists {
					switch value.(type) {
					case string, bool, int64, float64, int, int32:
						d[key] = value
					}
				}
			}
			if rawConditions, ok := status["conditions"].([]any); ok {
				conditions := make([]map[string]any, 0, min(len(rawConditions), 20))
				for _, raw := range rawConditions {
					condition, ok := raw.(map[string]any)
					if !ok || len(conditions) == 20 {
						continue
					}
					selected := map[string]any{}
					for _, key := range []string{"type", "status", "reason", "message"} {
						if value, ok := condition[key].(string); ok {
							selected[key] = cleanQueryText(value, token)
						}
					}
					conditions = append(conditions, selected)
				}
				d["conditions"] = conditions
			}
		}
	}
	return result, nil
}

func cleanQueryText(value, token string) string {
	if token != "" {
		value = strings.ReplaceAll(value, token, "[REDACTED]")
	}
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000]) + "…（已截断）"
	}
	return value
}
