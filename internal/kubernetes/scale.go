package kubernetes

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

type ScaleRequest struct {
	ClusterID string       `json:"cluster_id"`
	Kind      string       `json:"kind"`
	GVR       *ResourceRef `json:"gvr,omitempty"`
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	Replicas  *int32       `json:"replicas"`
}

type ScalePlan struct {
	ClusterID       string      `json:"cluster_id"`
	Kind            string      `json:"kind"`
	GVR             ResourceRef `json:"gvr"`
	Namespace       string      `json:"namespace"`
	Name            string      `json:"name"`
	CurrentReplicas int32       `json:"current_replicas"`
	TargetReplicas  int32       `json:"target_replicas"`
	ResourceVersion string      `json:"resource_version"`
	UID             string      `json:"uid,omitempty"`
	Source          string      `json:"source"`
}

type ScaleResult struct {
	ClusterID        string      `json:"cluster_id"`
	Kind             string      `json:"kind"`
	GVR              ResourceRef `json:"gvr"`
	Namespace        string      `json:"namespace"`
	Name             string      `json:"name"`
	PreviousReplicas int32       `json:"previous_replicas"`
	TargetReplicas   int32       `json:"target_replicas"`
	ObservedReplicas int32       `json:"observed_replicas"`
	Converged        bool        `json:"converged"`
	Submitted        bool        `json:"submitted"`
	Source           string      `json:"source"`
}

func (q *ScaleRequest) NormalizeAndValidate() error {
	q.ClusterID = strings.TrimSpace(q.ClusterID)
	q.Kind = strings.TrimSpace(q.Kind)
	q.Namespace = strings.TrimSpace(q.Namespace)
	q.Name = strings.TrimSpace(q.Name)
	if err := validateClusterID(q.ClusterID); err != nil {
		return err
	}
	if q.GVR == nil {
		if !kindPattern.MatchString(q.Kind) {
			return fmt.Errorf("未指定 gvr 时必须提供格式有效的 kind")
		}
	} else {
		q.GVR.Group = strings.TrimSpace(q.GVR.Group)
		q.GVR.Version = strings.TrimSpace(q.GVR.Version)
		q.GVR.Resource = strings.TrimSpace(q.GVR.Resource)
		if q.Kind != "" && !kindPattern.MatchString(q.Kind) {
			return fmt.Errorf("kind 格式无效")
		}
		if q.GVR.Group != "" && len(validation.IsDNS1123Subdomain(q.GVR.Group)) != 0 {
			return fmt.Errorf("gvr.group 格式无效")
		}
		if len(validation.IsDNS1123Label(q.GVR.Version)) != 0 || len(validation.IsDNS1123Subdomain(q.GVR.Resource)) != 0 || strings.Contains(q.GVR.Resource, "/") {
			return fmt.Errorf("gvr 必须指定有效的主资源 group、version 和 resource")
		}
	}
	if len(validation.IsDNS1123Label(q.Namespace)) != 0 {
		return fmt.Errorf("namespace 必须是有效且明确的命名空间")
	}
	if len(validation.IsDNS1123Subdomain(q.Name)) != 0 {
		return fmt.Errorf("name 必须是有效且精确的资源名称")
	}
	if q.Replicas == nil {
		return fmt.Errorf("replicas 必填")
	}
	if *q.Replicas < 0 {
		return fmt.Errorf("replicas 不能小于 0")
	}
	return nil
}

func (c *GatewayClient) PrepareScale(ctx context.Context, q ScaleRequest) (ScalePlan, error) {
	if err := q.NormalizeAndValidate(); err != nil {
		return ScalePlan{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client, resource, err := c.scaleClientAndResource(ctx, q)
	if err != nil {
		return ScalePlan{}, err
	}
	endpoint := client.Resource(resource.GVR).Namespace(q.Namespace)
	scale, err := endpoint.Get(ctx, q.Name, metav1.GetOptions{}, "scale")
	if err != nil {
		return ScalePlan{}, c.queryError("读取 Kubernetes Scale 子资源", err)
	}
	current, err := validateScaleObject(scale, q.Namespace, q.Name)
	if err != nil {
		return ScalePlan{}, err
	}
	if scale.GetResourceVersion() == "" {
		return ScalePlan{}, fmt.Errorf("Scale 子资源缺少 resourceVersion，无法建立安全的变更前置条件")
	}
	if scale.GetUID() == "" {
		return ScalePlan{}, fmt.Errorf("Scale 子资源缺少 UID，无法绑定确认目标")
	}
	q.Kind = resource.Kind
	q.GVR = &ResourceRef{Group: resource.GVR.Group, Version: resource.GVR.Version, Resource: resource.GVR.Resource}
	return ScalePlan{
		ClusterID: q.ClusterID, Kind: q.Kind, GVR: *q.GVR, Namespace: q.Namespace, Name: q.Name,
		CurrentReplicas: current, TargetReplicas: *q.Replicas, ResourceVersion: scale.GetResourceVersion(),
		UID: string(scale.GetUID()), Source: "kubernetes",
	}, nil
}

func (c *GatewayClient) ApplyScale(ctx context.Context, plan ScalePlan) (ScaleResult, error) {
	if err := plan.validate(); err != nil {
		return ScaleResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	target := plan.TargetReplicas
	request := ScaleRequest{ClusterID: plan.ClusterID, Kind: plan.Kind, GVR: &plan.GVR, Namespace: plan.Namespace, Name: plan.Name, Replicas: &target}
	client, resource, err := c.scaleClientAndResource(ctx, request)
	if err != nil {
		return ScaleResult{}, err
	}
	endpoint := client.Resource(resource.GVR).Namespace(plan.Namespace)
	currentScale, err := endpoint.Get(ctx, plan.Name, metav1.GetOptions{}, "scale")
	if err != nil {
		return ScaleResult{}, c.queryError("重新读取 Kubernetes Scale 子资源", err)
	}
	current, err := validateScaleObject(currentScale, plan.Namespace, plan.Name)
	if err != nil {
		return ScaleResult{}, err
	}
	if current != plan.CurrentReplicas || currentScale.GetResourceVersion() != plan.ResourceVersion || (plan.UID != "" && string(currentScale.GetUID()) != plan.UID) {
		return ScaleResult{}, fmt.Errorf("资源在确认期间已变化，请重新查询并确认扩缩容操作")
	}
	if err := unstructured.SetNestedField(currentScale.Object, int64(plan.TargetReplicas), "spec", "replicas"); err != nil {
		return ScaleResult{}, fmt.Errorf("设置目标副本数: %w", err)
	}
	updated, err := endpoint.Update(ctx, currentScale, metav1.UpdateOptions{}, "scale")
	if err != nil {
		return ScaleResult{}, c.queryError("更新 Kubernetes Scale 子资源", err)
	}
	desired, err := validateScaleObject(updated, plan.Namespace, plan.Name)
	if err != nil {
		return ScaleResult{}, err
	}
	if desired != plan.TargetReplicas {
		return ScaleResult{}, fmt.Errorf("Scale 更新响应的目标副本数与请求不一致")
	}
	if string(updated.GetUID()) != plan.UID {
		return ScaleResult{}, fmt.Errorf("Scale 更新响应的资源 UID 与确认目标不一致")
	}
	observed, found, err := unstructured.NestedInt64(updated.Object, "status", "replicas")
	if err != nil || !found || observed < 0 || observed > math.MaxInt32 {
		return ScaleResult{}, fmt.Errorf("Scale 更新响应缺少有效的 status.replicas")
	}
	return ScaleResult{
		ClusterID: plan.ClusterID, Kind: resource.Kind, GVR: plan.GVR, Namespace: plan.Namespace, Name: plan.Name,
		PreviousReplicas: plan.CurrentReplicas, TargetReplicas: plan.TargetReplicas, ObservedReplicas: int32(observed),
		Converged: int32(observed) == plan.TargetReplicas, Submitted: true, Source: "kubernetes",
	}, nil
}

func (c *GatewayClient) scaleClientAndResource(ctx context.Context, q ScaleRequest) (dynamic.Interface, discoveredResource, error) {
	cfg, err := c.restConfig(q.ClusterID)
	if err != nil {
		return nil, discoveredResource{}, err
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, discoveredResource{}, fmt.Errorf("创建 Kubernetes Scale 客户端: %w", err)
	}
	core, err := c.coreClient(q.ClusterID)
	if err != nil {
		return nil, discoveredResource{}, err
	}
	query := QueryRequest{ClusterID: q.ClusterID, Action: "get", Kind: q.Kind, GVR: q.GVR, Namespace: q.Namespace, Name: q.Name}
	resource, err := c.resolveResource(ctx, core, query)
	if err != nil {
		return nil, discoveredResource{}, err
	}
	if !resource.Namespaced {
		return nil, discoveredResource{}, fmt.Errorf("kubernetes_scale 仅支持命名空间级资源")
	}
	if !resource.Scale {
		return nil, discoveredResource{}, fmt.Errorf("资源 %s 未提供 scale 子资源", formatGVR(resource.GVR))
	}
	return client, resource, nil
}

func validateScaleObject(item *unstructured.Unstructured, namespace, name string) (int32, error) {
	if item.GetAPIVersion() != "autoscaling/v1" || item.GetKind() != "Scale" {
		return 0, fmt.Errorf("Scale 子资源返回了非 autoscaling/v1 Scale 对象")
	}
	if item.GetName() != name || item.GetNamespace() != namespace {
		return 0, fmt.Errorf("Scale 子资源返回的目标身份与请求不一致")
	}
	replicas, found, err := unstructured.NestedInt64(item.Object, "spec", "replicas")
	if err != nil || !found || replicas < 0 || replicas > math.MaxInt32 {
		return 0, fmt.Errorf("Scale 子资源缺少有效的 spec.replicas")
	}
	return int32(replicas), nil
}

func (p ScalePlan) validate() error {
	target := p.TargetReplicas
	q := ScaleRequest{ClusterID: p.ClusterID, Kind: p.Kind, GVR: &p.GVR, Namespace: p.Namespace, Name: p.Name, Replicas: &target}
	if err := q.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("扩缩容计划无效: %w", err)
	}
	if p.CurrentReplicas < 0 || p.ResourceVersion == "" || p.UID == "" || p.Source != "kubernetes" {
		return fmt.Errorf("扩缩容计划缺少有效的前置条件")
	}
	return nil
}
