package sandbox

import "testing"

func TestParsePodStatusCountsReadinessAndOOM(t *testing.T) {
	data := []byte(`{"items":[{"status":{"phase":"Running","containerStatuses":[{"ready":true}]}},{"status":{"phase":"Running","containerStatuses":[{"ready":false,"lastState":{"terminated":{"reason":"OOMKilled"}}}]}}]}`)
	status, err := parsePodStatus(2, data)
	if err != nil || status.Desired != 2 || status.Healthy != 1 || status.OOMKills != 1 || status.ObservedAt.IsZero() {
		t.Fatalf("pod status not parsed: %+v %v", status, err)
	}
	if _, err := parsePodStatus(2, []byte(`{"items":`)); err == nil {
		t.Fatal("malformed Kubernetes response accepted")
	}
}
