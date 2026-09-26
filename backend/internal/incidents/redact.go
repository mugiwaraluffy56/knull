package incidents

import "strings"

// sensitiveKeyFragments are substrings that mark a data field as sensitive. Any
// event data key containing one of these (case-insensitive) is masked before the
// event is persisted or rendered, so credentials and tokens never reach the
// timeline.
var sensitiveKeyFragments = []string{
	"password", "passwd", "secret", "token", "apikey", "api_key",
	"authorization", "auth", "credential", "private", "bearer",
	"kubeconfig", "cookie", "session",
}

const redactedPlaceholder = "[redacted]"

// redactData returns a copy of data with sensitive values masked. It never
// mutates the input. Nested maps are redacted recursively.
func redactData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		if isSensitiveKey(k) {
			out[k] = redactedPlaceholder
			continue
		}
		switch child := v.(type) {
		case map[string]any:
			out[k] = redactData(child)
		default:
			out[k] = v
		}
	}
	return out
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, frag := range sensitiveKeyFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}
