package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
)

const discoveryCacheTTL = 10 * time.Minute

type ResourceRef struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
}

type discoveredResource struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
	Verbs      map[string]bool
}

type discoveryCacheEntry struct {
	expiresAt time.Time
	complete  bool
	byKind    map[string][]discoveredResource
	byGVR     map[string]discoveredResource
}

type ambiguousResourceError struct {
	kind       string
	candidates []discoveredResource
}

func (e *ambiguousResourceError) Error() string {
	values := make([]string, 0, len(e.candidates))
	for _, candidate := range e.candidates {
		values = append(values, formatGVR(candidate.GVR))
	}
	return fmt.Sprintf("Kind %s 在多个 API Group 中存在，请通过 gvr 精确指定：%s", e.kind, strings.Join(values, "、"))
}

func formatGVR(gvr schema.GroupVersionResource) string {
	if gvr.Group == "" {
		return gvr.Version + "/" + gvr.Resource
	}
	return gvr.Group + "/" + gvr.Version + "/" + gvr.Resource
}

func discoveryKey(gvr schema.GroupVersionResource) string {
	return gvr.Group + "\x00" + gvr.Version + "\x00" + gvr.Resource
}

func (c *GatewayClient) resolveResource(ctx context.Context, core *coreclient.CoreV1Client, request QueryRequest) (discoveredResource, error) {
	if request.GVR != nil {
		gvr := schema.GroupVersionResource{Group: request.GVR.Group, Version: request.GVR.Version, Resource: request.GVR.Resource}
		if cached, ok := c.cachedResourceByGVR(request.ClusterID, gvr); ok {
			return validateDiscoveredResource(cached, request)
		}
		resource, err := c.discoverExactResource(ctx, core, gvr)
		if err != nil {
			return discoveredResource{}, err
		}
		c.cacheExactResource(request.ClusterID, resource)
		return validateDiscoveredResource(resource, request)
	}

	if cached, ok := c.cachedResourcesByKind(request.ClusterID, request.Kind); ok {
		return chooseDiscoveredResource(request.Kind, cached, request)
	}
	entry, incomplete, err := c.discoverPreferredResources(ctx, core)
	if err != nil {
		return discoveredResource{}, err
	}
	if !incomplete {
		entry.complete = true
		c.discoveryMu.Lock()
		c.discoveryCache[request.ClusterID] = entry
		c.discoveryMu.Unlock()
	}
	matches := entry.byKind[strings.ToLower(request.Kind)]
	if len(matches) == 0 && incomplete {
		return discoveredResource{}, fmt.Errorf("部分 API Group Discovery 失败，且未能确定 Kind %s 对应的 GVR", request.Kind)
	}
	return chooseDiscoveredResource(request.Kind, matches, request)
}

func chooseDiscoveredResource(kind string, matches []discoveredResource, request QueryRequest) (discoveredResource, error) {
	if len(matches) == 0 {
		return discoveredResource{}, fmt.Errorf("目标集群的 Preferred API Resources 中不存在 Kind %s", kind)
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool { return formatGVR(matches[i].GVR) < formatGVR(matches[j].GVR) })
		return discoveredResource{}, &ambiguousResourceError{kind: kind, candidates: matches}
	}
	return validateDiscoveredResource(matches[0], request)
}

func validateDiscoveredResource(resource discoveredResource, request QueryRequest) (discoveredResource, error) {
	if request.Kind != "" && request.Kind != resource.Kind {
		return discoveredResource{}, fmt.Errorf("指定的 kind %s 与 GVR 对应的 Kind %s 不一致", request.Kind, resource.Kind)
	}
	if !resource.Verbs[request.Action] {
		return discoveredResource{}, fmt.Errorf("资源 %s 不支持 %s 操作", formatGVR(resource.GVR), request.Action)
	}
	if resource.GVR.Group == "" && resource.GVR.Resource == "secrets" {
		return discoveredResource{}, fmt.Errorf("kubernetes_query 不允许读取 Secret")
	}
	return resource, nil
}

func (c *GatewayClient) cachedResourcesByKind(clusterID, kind string) ([]discoveredResource, bool) {
	c.discoveryMu.RLock()
	defer c.discoveryMu.RUnlock()
	entry, ok := c.discoveryCache[clusterID]
	if !ok || !entry.complete || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	matches := entry.byKind[strings.ToLower(kind)]
	return append([]discoveredResource(nil), matches...), true
}

func (c *GatewayClient) cachedResourceByGVR(clusterID string, gvr schema.GroupVersionResource) (discoveredResource, bool) {
	c.discoveryMu.RLock()
	defer c.discoveryMu.RUnlock()
	entry, ok := c.discoveryCache[clusterID]
	if !ok || time.Now().After(entry.expiresAt) {
		return discoveredResource{}, false
	}
	resource, ok := entry.byGVR[discoveryKey(gvr)]
	return resource, ok
}

func (c *GatewayClient) cacheExactResource(clusterID string, resource discoveredResource) {
	c.discoveryMu.Lock()
	defer c.discoveryMu.Unlock()
	entry, ok := c.discoveryCache[clusterID]
	if !ok || time.Now().After(entry.expiresAt) {
		entry = newDiscoveryCacheEntry()
	}
	entry.byGVR[discoveryKey(resource.GVR)] = resource
	c.discoveryCache[clusterID] = entry
}

func newDiscoveryCacheEntry() discoveryCacheEntry {
	return discoveryCacheEntry{expiresAt: time.Now().Add(discoveryCacheTTL), byKind: make(map[string][]discoveredResource), byGVR: make(map[string]discoveredResource)}
}

func addDiscoveredResources(entry *discoveryCacheEntry, list *metav1.APIResourceList) error {
	gv, err := schema.ParseGroupVersion(list.GroupVersion)
	if err != nil {
		return fmt.Errorf("Discovery 返回无效 groupVersion")
	}
	for _, apiResource := range list.APIResources {
		if apiResource.Name == "" || apiResource.Kind == "" || strings.Contains(apiResource.Name, "/") {
			continue
		}
		verbs := make(map[string]bool, len(apiResource.Verbs))
		for _, verb := range apiResource.Verbs {
			verbs[verb] = true
		}
		resource := discoveredResource{GVR: gv.WithResource(apiResource.Name), Kind: apiResource.Kind, Namespaced: apiResource.Namespaced, Verbs: verbs}
		entry.byKind[strings.ToLower(resource.Kind)] = append(entry.byKind[strings.ToLower(resource.Kind)], resource)
		entry.byGVR[discoveryKey(resource.GVR)] = resource
	}
	return nil
}

func (c *GatewayClient) discoverPreferredResources(ctx context.Context, core *coreclient.CoreV1Client) (discoveryCacheEntry, bool, error) {
	entry := newDiscoveryCacheEntry()
	var coreResources metav1.APIResourceList
	if err := core.RESTClient().Get().AbsPath("/api/v1").Do(ctx).Into(&coreResources); err != nil {
		return discoveryCacheEntry{}, false, c.queryError("发现 Kubernetes core/v1 资源", err)
	}
	if coreResources.GroupVersion != "v1" {
		return discoveryCacheEntry{}, false, fmt.Errorf("core API Discovery 返回的版本不是 v1")
	}
	if err := addDiscoveredResources(&entry, &coreResources); err != nil {
		return discoveryCacheEntry{}, false, err
	}

	var groups metav1.APIGroupList
	if err := core.RESTClient().Get().AbsPath("/apis").Do(ctx).Into(&groups); err != nil {
		return entry, true, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	incomplete := false
	for _, group := range groups.Groups {
		groupVersion := group.PreferredVersion.GroupVersion
		if groupVersion == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				mu.Lock()
				incomplete = true
				mu.Unlock()
				return
			}
			var resources metav1.APIResourceList
			if err := core.RESTClient().Get().AbsPath("/apis/" + groupVersion).Do(ctx).Into(&resources); err != nil || resources.GroupVersion != groupVersion {
				mu.Lock()
				incomplete = true
				mu.Unlock()
				return
			}
			mu.Lock()
			if err := addDiscoveredResources(&entry, &resources); err != nil {
				incomplete = true
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return entry, incomplete, nil
}

func (c *GatewayClient) discoverExactResource(ctx context.Context, core *coreclient.CoreV1Client, gvr schema.GroupVersionResource) (discoveredResource, error) {
	path := "/api/" + gvr.Version
	if gvr.Group != "" {
		path = "/apis/" + gvr.Group + "/" + gvr.Version
	}
	var resources metav1.APIResourceList
	if err := core.RESTClient().Get().AbsPath(path).Do(ctx).Into(&resources); err != nil {
		return discoveredResource{}, c.queryError("发现指定 GVR", err)
	}
	if resources.GroupVersion != gvr.GroupVersion().String() {
		return discoveredResource{}, fmt.Errorf("指定 GVR 的 Discovery 版本不一致")
	}
	entry := newDiscoveryCacheEntry()
	if err := addDiscoveredResources(&entry, &resources); err != nil {
		return discoveredResource{}, err
	}
	resource, ok := entry.byGVR[discoveryKey(gvr)]
	if !ok {
		return discoveredResource{}, fmt.Errorf("目标集群不存在 GVR %s", formatGVR(gvr))
	}
	return resource, nil
}
