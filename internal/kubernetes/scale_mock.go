package kubernetes

import (
	"context"
	"fmt"
)

func (c *MockClient) PrepareScale(ctx context.Context, q ScaleRequest) (ScalePlan, error) {
	if err := q.NormalizeAndValidate(); err != nil {
		return ScalePlan{}, err
	}
	if err := ctx.Err(); err != nil {
		return ScalePlan{}, err
	}
	if q.Kind != "Deployment" && q.Kind != "StatefulSet" {
		return ScalePlan{}, fmt.Errorf("Mock 中的 %s 未提供 scale 子资源", q.Kind)
	}
	resource := "deployments"
	if q.Kind == "StatefulSet" {
		resource = "statefulsets"
	}
	if q.GVR == nil {
		q.GVR = &ResourceRef{Group: "apps", Version: "v1", Resource: resource}
	}
	return ScalePlan{
		ClusterID: q.ClusterID, Kind: q.Kind, GVR: *q.GVR, Namespace: q.Namespace, Name: q.Name,
		CurrentReplicas: 1, TargetReplicas: *q.Replicas, ResourceVersion: "mock-1", UID: "mock-uid", Source: "mock",
	}, nil
}

func (c *MockClient) ApplyScale(ctx context.Context, plan ScalePlan) (ScaleResult, error) {
	if err := ctx.Err(); err != nil {
		return ScaleResult{}, err
	}
	if plan.Source != "mock" || plan.ResourceVersion != "mock-1" {
		return ScaleResult{}, fmt.Errorf("Mock 扩缩容计划已失效")
	}
	return ScaleResult{
		ClusterID: plan.ClusterID, Kind: plan.Kind, GVR: plan.GVR, Namespace: plan.Namespace, Name: plan.Name,
		PreviousReplicas: plan.CurrentReplicas, TargetReplicas: plan.TargetReplicas, ObservedReplicas: plan.TargetReplicas,
		Converged: true, Submitted: true, Source: "mock",
	}, nil
}
