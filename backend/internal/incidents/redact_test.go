package incidents

import "testing"

func TestRedactMasksSensitiveKeys(t *testing.T) {
	in := map[string]any{
		"namespace":     "shop",
		"token":         "ghp_abc",
		"apiKey":        "sk-123",
		"password":      "hunter2",
		"errorRate":     "38%",
		"Authorization": "Bearer x",
	}
	out := redactData(in)
	if out["namespace"] != "shop" || out["errorRate"] != "38%" {
		t.Fatal("non-sensitive values must be preserved")
	}
	for _, k := range []string{"token", "apiKey", "password", "Authorization"} {
		if out[k] != redactedPlaceholder {
			t.Errorf("%s not redacted: %v", k, out[k])
		}
	}
	// Input is not mutated.
	if in["token"] != "ghp_abc" {
		t.Fatal("redactData must not mutate input")
	}
}

func TestRedactNested(t *testing.T) {
	in := map[string]any{
		"outer": map[string]any{"secret": "s", "ok": "v"},
	}
	out := redactData(in)
	nested := out["outer"].(map[string]any)
	if nested["secret"] != redactedPlaceholder || nested["ok"] != "v" {
		t.Fatalf("nested redaction wrong: %v", nested)
	}
}

func TestRedactNil(t *testing.T) {
	if redactData(nil) != nil {
		t.Fatal("nil data should stay nil")
	}
}
