package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const maxFullResourceBytes = 64 * 1024

func formatQueryResource(item unstructured.Unstructured, output, token string) (ResourceSummary, error) {
	if output != "full" {
		return summarizeResource(item, token)
	}
	// 直接使用 unstructured 对象，保留任意 CR 的 spec/status；不经过内置类型转换或摘要提取。
	encoded, err := json.Marshal(item.Object)
	if err != nil {
		return ResourceSummary{}, fmt.Errorf("完整资源无法序列化为 JSON")
	}
	if len(encoded) > maxFullResourceBytes {
		return ResourceSummary{}, fmt.Errorf("完整资源超过 64 KiB 输出上限（%d 字节），未返回部分内容；可使用其他客户端查看完整对象", len(encoded))
	}
	copy := item.DeepCopy()
	redacted := redactResourceValue(copy.Object, token)
	encoded, err = json.Marshal(copy.Object)
	if err != nil {
		return ResourceSummary{}, fmt.Errorf("完整资源无法序列化为 JSON")
	}
	if len(encoded) > maxFullResourceBytes {
		return ResourceSummary{}, fmt.Errorf("脱敏后的完整资源超过 64 KiB 输出上限，未返回部分内容")
	}
	return ResourceSummary{
		APIVersion: item.GetAPIVersion(), Kind: item.GetKind(), Name: item.GetName(), Namespace: item.GetNamespace(),
		Resource: copy.Object, Redacted: redacted,
	}, nil
}

// 脱敏常见凭证字段、敏感环境变量、可能嵌入整份凭证配置的 annotation，以及已知网关 Token。
// 不删除资源结构，不截断普通字段；发生替换时由 redacted 明确标记。
func redactResourceValue(value any, token string) bool {
	changed := false
	switch node := value.(type) {
	case map[string]any:
		sensitiveValue := false
		if name, ok := node["name"].(string); ok {
			sensitiveValue = sensitiveResourceKey(name)
		}
		for key, child := range node {
			if sensitiveResourceKey(key) || key == "kubectl.kubernetes.io/last-applied-configuration" || (key == "value" && sensitiveValue) {
				node[key] = "[REDACTED]"
				changed = true
				continue
			}
			if text, ok := child.(string); ok && token != "" && strings.Contains(text, token) {
				node[key] = strings.ReplaceAll(text, token, "[REDACTED]")
				changed = true
			} else if redactResourceValue(child, token) {
				changed = true
			}
		}
	case []any:
		for i, child := range node {
			if text, ok := child.(string); ok && token != "" && strings.Contains(text, token) {
				node[i] = strings.ReplaceAll(text, token, "[REDACTED]")
				changed = true
			} else if redactResourceValue(child, token) {
				changed = true
			}
		}
	}
	return changed
}

func sensitiveResourceKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	switch normalized {
	case "password", "passwd", "pwd", "token", "secret", "credentials", "authorization", "kubeconfig", "privatekey", "clientsecret", "apikey":
		return true
	}
	for _, suffix := range []string{"password", "passwd", "accesstoken", "refreshtoken", "apitoken", "apikey", "privatekey", "clientsecret"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
