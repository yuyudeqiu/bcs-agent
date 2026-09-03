package kubernetes

import (
	"regexp"
	"strings"
)

var logPrivateKey = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|$)`)
var logAuthorization = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
var logCredential = regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_.-]*(?:password|passwd|access[_-]?token|refresh[_-]?token|api[_-]?token|api[_-]?key|client[_-]?secret)|token|secret|authorization|credentials)["']?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}]+)`)

// 日志是非结构化文本，只覆盖已知 Token、常见赋值/JSON 凭证、认证头和 PEM 私钥。
// 不能保证识别业务自定义敏感内容，结果中的 note 会明确说明这一范围。
func redactLogText(text, token string) (string, bool) {
	original := text
	if token != "" {
		text = strings.ReplaceAll(text, token, "[REDACTED]")
	}
	text = logPrivateKey.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
	text = logAuthorization.ReplaceAllString(text, "${1} [REDACTED]")
	text = logCredential.ReplaceAllString(text, "${1}[REDACTED]")
	return text, text != original
}
