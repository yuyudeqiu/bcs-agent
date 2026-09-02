package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (c *MockClient) Query(ctx context.Context, q QueryRequest) (QueryResult, error) {
	if err := q.NormalizeAndValidate(); err != nil {
		return QueryResult{}, err
	}
	nodes, err := c.GetNodeSummary(ctx, q.ClusterID)
	if err != nil {
		return QueryResult{}, err
	}
	result := newQueryResult(q, "mock")
	var objects []unstructured.Unstructured
	add := func(name, namespace, body string) {
		var object map[string]any
		// 下方均为固定的 Mock fixture，JSON 错误属于开发错误。
		if err := json.Unmarshal([]byte(body), &object); err != nil {
			panic(err)
		}
		item := unstructured.Unstructured{Object: object}
		item.SetKind(q.Kind)
		item.SetAPIVersion(queryResources[q.Kind].groupVersion.String())
		item.SetName(name)
		item.SetNamespace(namespace)
		objects = append(objects, item)
	}
	switch q.Kind {
	case "Node":
		for i := 0; i < nodes.TotalNodes; i++ {
			ready := "True"
			if i >= nodes.ReadyNodes {
				ready = "False"
			}
			add(fmt.Sprintf("worker-%02d", i+1), "", fmt.Sprintf(`{"status":{"nodeInfo":{"kubeletVersion":"v1.28.0"},"conditions":[{"type":"Ready","status":%q}]}}`, ready))
		}
	case "Namespace":
		for _, namespace := range []string{"default", "production"} {
			add(namespace, "", `{"status":{"phase":"Active"}}`)
		}
	case "Pod":
		add("web-0", "default", `{"spec":{"nodeName":"worker-01","containers":[{"name":"web"}]},"status":{"phase":"Running","containerStatuses":[{"name":"web","ready":true,"restartCount":0,"state":{"running":{}}}]}}`)
		add("web-1", "production", `{"spec":{"containers":[{"name":"web"}]},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable"}]}}`)
	case "Deployment":
		add("web", "default", `{"spec":{"replicas":1},"status":{"readyReplicas":1,"availableReplicas":1,"updatedReplicas":1}}`)
		add("web", "production", `{"spec":{"replicas":1},"status":{"readyReplicas":0,"availableReplicas":0,"updatedReplicas":1}}`)
	case "Event":
		add("web-1.failed-scheduling", "production", `{"type":"Warning","reason":"FailedScheduling","message":"Mock: insufficient CPU","count":1,"involvedObject":{"kind":"Pod","namespace":"production","name":"web-1"}}`)
	}
	matches := []ResourceSummary{}
	for _, item := range objects {
		if q.Namespace != "" && item.GetNamespace() != q.Namespace {
			continue
		}
		if q.Name != "" && item.GetName() != q.Name {
			continue
		}
		summary, err := summarizeResource(item, "")
		if err != nil {
			return QueryResult{}, err
		}
		matches = append(matches, summary)
	}
	if q.Action == "get" {
		if len(matches) == 0 {
			return QueryResult{}, apierrors.NewNotFound(schema.GroupResource{Group: queryResources[q.Kind].groupVersion.Group, Resource: q.Kind}, q.Name)
		}
		result.Items = matches
	} else {
		offset := 0
		if q.Continue != "" {
			value, err := strconv.Atoi(q.Continue)
			if err != nil || value < 1 || value >= len(matches) {
				return QueryResult{}, fmt.Errorf("无效的 Mock continue 标记")
			}
			offset = value
		}
		end := min(offset+int(q.Limit), len(matches))
		result.Items = matches[offset:end]
		if end < len(matches) {
			result.HasMore = true
			result.Continue = strconv.Itoa(end)
		}
	}
	result.Count = len(result.Items)
	return result, nil
}
