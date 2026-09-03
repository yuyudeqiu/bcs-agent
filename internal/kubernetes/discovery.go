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

// Discovery 映射按集群缓存；TTL 到期后重新发现，以反映 CRD 或 API 版本变化。
const discoveryCacheTTL = 10 * time.Minute

// ResourceRef 是工具参数和查询结果中的 GVR（Group、Version、Resource）。
// core API 的 Group 为空，Resource 使用 Discovery 返回的复数资源名。
type ResourceRef struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
}

// discoveredResource 保存构造资源请求所需的元信息，避免从 Kind 猜测路径或作用域。
type discoveredResource struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
	Verbs      map[string]bool
}

// discoveryCacheEntry 属于单个集群，两个索引服务于不同的解析入口。
// byKind 只收录 preferred 版本；byGVR 还可包含显式查询过的非 preferred 版本。
type discoveryCacheEntry struct {
	expiresAt time.Time
	complete  bool // 是否已完成 core/v1 和各 API Group preferred 版本的发现；精确 GVR 查询不置位。
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

// 使用零字节分隔各段，使空 Group 等情况也有明确边界；此键仅用于内部索引。
func discoveryKey(gvr schema.GroupVersionResource) string {
	return gvr.Group + "\x00" + gvr.Version + "\x00" + gvr.Resource
}

// resolveResource 将工具请求解析为集群实际提供的资源，并校验 Kind 和操作能力。
// 显式 GVR 只查指定版本；仅传 Kind 时按 preferred 版本查找，多个候选交由调用方消歧。
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
		// 部分发现结果只供本次查询使用，不能缓存为完整映射，否则会掩盖未发现的资源。
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

// chooseDiscoveredResource 只接受唯一候选，不按 Group 名或返回顺序擅自选择。
func chooseDiscoveredResource(kind string, matches []discoveredResource, request QueryRequest) (discoveredResource, error) {
	if len(matches) == 0 {
		return discoveredResource{}, fmt.Errorf("目标集群的 Preferred API Resources 中不存在 Kind %s", kind)
	}
	if len(matches) > 1 {
		// 各 Group 并发发现，返回顺序不固定；排序使歧义提示保持稳定。
		sort.Slice(matches, func(i, j int) bool { return formatGVR(matches[i].GVR) < formatGVR(matches[j].GVR) })
		return discoveredResource{}, &ambiguousResourceError{kind: kind, candidates: matches}
	}
	return validateDiscoveredResource(matches[0], request)
}

// 缓存命中和实时发现都经过同一校验，避免显式 GVR 绕过 Kind、操作和 Secret 限制。
func validateDiscoveredResource(resource discoveredResource, request QueryRequest) (discoveredResource, error) {
	// Kind 来自用户自然语言时大小写可能不规范；Discovery 返回值才是执行请求时使用的规范 Kind。
	if request.Kind != "" && !strings.EqualFold(request.Kind, resource.Kind) {
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

// 只有完整且未过期的映射才能回答 Kind 查询；命中时空切片表示该 Kind 不在映射中。
func (c *GatewayClient) cachedResourcesByKind(clusterID, kind string) ([]discoveredResource, bool) {
	c.discoveryMu.RLock()
	defer c.discoveryMu.RUnlock()
	entry, ok := c.discoveryCache[clusterID]
	if !ok || !entry.complete || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	matches := entry.byKind[strings.ToLower(kind)]
	// 调用方会对候选排序，复制切片以免修改共享缓存；资源内的 Verbs 映射只读。
	return append([]discoveredResource(nil), matches...), true
}

// 精确 GVR 命中不要求 complete：单个版本的 Discovery 足以确认该资源存在。
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

// 精确查询只补充 byGVR，不写入 byKind，也不延长已有条目的 TTL。
// 指定版本可能不是 preferred 版本，不能让它影响后续仅按 Kind 的解析。
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

// addDiscoveredResources 将某个 GroupVersion 的资源列表写入两个索引。
// 本函数不加锁；并发收集时由调用方保护 entry。
func addDiscoveredResources(entry *discoveryCacheEntry, list *metav1.APIResourceList) error {
	gv, err := schema.ParseGroupVersion(list.GroupVersion)
	if err != nil {
		return fmt.Errorf("Discovery 返回无效 groupVersion")
	}
	for _, apiResource := range list.APIResources {
		// 跳过缺少身份信息的条目及 pods/log、deployments/scale 等子资源。
		// 通用 list/get 只处理主资源，不将子资源当成独立 Kind 候选。
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

// discoverPreferredResources 先发现 core/v1，再并发读取各 API Group 的 preferred 版本。
// 第二个返回值表示发现不完整：core/v1 失败直接报错，扩展 API 失败则保留已发现的结果，
// 由 resolveResource 尝试匹配本次目标，但不将其作为完整映射缓存。
func (c *GatewayClient) discoverPreferredResources(ctx context.Context, core *coreclient.CoreV1Client) (discoveryCacheEntry, bool, error) {
	entry := newDiscoveryCacheEntry()
	var coreResources metav1.APIResourceList
	// 使用带集群网关前缀的 RESTClient，Discovery 请求也经 BCS 转发到目标集群。
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
	// 最多同时请求 8 个 GroupVersion；等待名额的协程也响应 context 取消。
	semaphore := make(chan struct{}, 8)
	incomplete := false
	for _, group := range groups.Groups {
		// 每个 Group 只取服务端声明的 preferred 版本，避免同一资源的多个版本形成重复候选。
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
			// 合并资源索引和更新 incomplete 共用一把锁，防止并发写 map。
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

// discoverExactResource 只读取指定 GroupVersion 的 Discovery 并精确匹配资源名。
// 版本或资源不存在时返回错误，不回退到 preferred 版本；core 与扩展 API 使用不同路径。
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
