package validation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestPrometheusObserverRequiresScopedCompleteSeries(t *testing.T) {
	namespace := "knull-run-" + strings.Repeat("a", 20)
	seen := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		query := r.URL.Query()
		if r.URL.Path != "/api/v1/query_range" || !strings.Contains(query.Get("query"), namespace) || query.Get("step") != "10s" {
			t.Errorf("unscoped query: %s", r.URL.String())
		}
		start, _ := strconv.ParseFloat(query.Get("start"), 64)
		value := "0.005"
		if strings.Contains(query.Get("query"), "latency") {
			value = "190"
		}
		samples := make([][2]any, 10)
		for i := range samples {
			samples[i] = [2]any{start + float64(i*10), value}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"values": samples}}}})
	}))
	defer server.Close()
	o := PrometheusObserver{BaseURL: server.URL, ErrorRateQuery: `load_errors{namespace="{{namespace}}"}`, P95MillisecondsQuery: `load_latency{namespace="{{namespace}}"}`}
	result, err := o.ObserveMetrics(t.Context(), namespace)
	if err != nil || result.ErrorRate != 0.005 || result.P95Milliseconds != 190 || result.Samples != 10 || seen != 2 {
		t.Fatalf("unexpected observation: %+v %v calls=%d", result, err, seen)
	}
	o.ErrorRateQuery = "vector(0)"
	if _, err := o.ObserveMetrics(t.Context(), namespace); err == nil {
		t.Fatal("unscoped query accepted")
	}
	if seen != 2 {
		t.Fatal("unscoped query reached Prometheus")
	}
}

func TestPrometheusObserverRejectsMissingSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer server.Close()
	o := PrometheusObserver{BaseURL: server.URL, ErrorRateQuery: `errors{namespace="{{namespace}}"}`, P95MillisecondsQuery: `latency{namespace="{{namespace}}"}`}
	if _, err := o.ObserveMetrics(t.Context(), "knull-run-"+strings.Repeat("b", 20)); err == nil {
		t.Fatal("missing observations accepted")
	}
}
