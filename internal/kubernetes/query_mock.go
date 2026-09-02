package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
	resources := map[string]discoveredResource{}
	for kind, resource := range queryResources {
		gvr := resource.groupVersion.WithResource(strings.ToLower(kind) + "s")
		if kind == "Namespace" {
			gvr.Resource = "namespaces"
		}
		resources[kind] = discoveredResource{GVR: gvr, Kind: kind, Namespaced: resource.namespaced, Verbs: map[string]bool{"list": true, "get": true}}
	}
	resources["CronJob"] = discoveredResource{GVR: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, Kind: "CronJob", Namespaced: true, Verbs: map[string]bool{"list": true, "get": true}}
	resources["Widget"] = discoveredResource{GVR: schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}, Kind: "Widget", Namespaced: true, Verbs: map[string]bool{"list": true, "get": true}}
	var resource discoveredResource
	if q.GVR != nil {
		for _, candidate := range resources {
			if candidate.GVR == (schema.GroupVersionResource{Group: q.GVR.Group, Version: q.GVR.Version, Resource: q.GVR.Resource}) {
				resource = candidate
				break
			}
		}
	} else {
		resource = resources[q.Kind]
	}
	if resource.Kind == "" {
		return QueryResult{}, fmt.Errorf("Mock 中不存在请求的 Kubernetes 资源")
	}
	if _, err := validateDiscoveredResource(resource, q); err != nil {
		return QueryResult{}, err
	}
	if !resource.Namespaced && (q.Namespace != "" || q.AllNamespaces) {
		return QueryResult{}, fmt.Errorf("%s 是集群级资源，不能指定 namespace 或 all_namespaces", resource.Kind)
	}
	if resource.Namespaced && q.Namespace == "" && !q.AllNamespaces {
		return QueryResult{}, fmt.Errorf("%s 需要明确 namespace；跨命名空间列表查询请指定 all_namespaces=true", resource.Kind)
	}
	q.Kind = resource.Kind
	q.GVR = &ResourceRef{Group: resource.GVR.Group, Version: resource.GVR.Version, Resource: resource.GVR.Resource}
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
		item.SetAPIVersion(resource.GVR.GroupVersion().String())
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
	case "CronJob":
		add("cleanup", "default", `{"status":{"lastScheduleTime":"2026-01-01T00:00:00Z"}}`)
	case "Widget":
		add("example", "default", `{"status":{"phase":"Ready","conditions":[{"type":"Available","status":"True","reason":"Reconciled"}]}}`)
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
			return QueryResult{}, apierrors.NewNotFound(resource.GVR.GroupResource(), q.Name)
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
