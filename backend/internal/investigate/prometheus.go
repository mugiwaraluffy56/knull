package investigate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// PrometheusInvestigator runs bounded, read-only queries through the configured
// Prometheus MCP server. It only queries when the service has an explicit label
// selector, keeping a missing mapping from turning into an unscoped fleet query.
type PrometheusInvestigator struct {
	client toolCaller
	now    func() time.Time
}

// NewPrometheusInvestigator builds a PrometheusInvestigator over an MCP client.
func NewPrometheusInvestigator(client toolCaller) *PrometheusInvestigator {
	return &PrometheusInvestigator{client: client, now: time.Now}
}

const sourcePrometheus = "prometheus"

var prometheusLabelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type prometheusQuery struct {
	name  string
	query string
	unit  string
}

// Inspect records an alert snapshot and a 15-minute range for common service
// signals. Metric families follow the conventional Kubernetes and HTTP
// exposition names; absent series are recorded as unavailable, never as zero.
func (p *PrometheusInvestigator) Inspect(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, t Target) error {
	target := t.ServiceKey + "/" + t.Environment
	labels := make(map[string]string, len(t.PrometheusLabels)+1)
	for key, value := range t.PrometheusLabels {
		labels[key] = value
	}
	if configured, ok := labels["environment"]; ok && configured != t.Environment {
		return p.recordAllUnavailable(ctx, rec, incidentID, target, fmt.Errorf("Prometheus environment label does not match incident environment"))
	}
	labels["environment"] = t.Environment
	selector, err := labelSelector(labels)
	if err != nil {
		return p.recordAllUnavailable(ctx, rec, incidentID, target, err)
	}
	if len(t.PrometheusLabels) == 0 {
		return p.recordAllUnavailable(ctx, rec, incidentID, target, fmt.Errorf("no Prometheus labels configured for %s in %s", t.ServiceKey, t.Environment))
	}

	now := p.now().UTC()
	start := now.Add(-15 * time.Minute)
	window := map[string]any{
		"start_time": start.Format(time.RFC3339),
		"end_time":   now.Format(time.RFC3339),
		"step":       "1m",
	}

	// ALERTS is an instant vector. An empty result means there are no active
	// matching alerts and is therefore a successful observation with count 0.
	alertQuery := "ALERTS" + selector
	if err := p.query(ctx, rec, incidentID, target, "active alerts", "query", alertQuery, map[string]any{
		"query":            alertQuery,
		"timestamp":        now.Format(time.RFC3339),
		"truncation_limit": 50,
	}, "alerts", now, true); err != nil {
		return err
	}

	queries := []prometheusQuery{
		{name: "cpu usage", query: "sum(rate(container_cpu_usage_seconds_total" + withMatchers(selector, `container!="",container!="POD"`) + "[5m]))", unit: "cores"},
		{name: "memory usage", query: "sum(container_memory_working_set_bytes" + withMatchers(selector, `container!="",container!="POD"`) + ")", unit: "bytes"},
		{name: "cpu saturation", query: "100 * sum(rate(container_cpu_usage_seconds_total" + withMatchers(selector, `container!="",container!="POD"`) + "[5m])) / sum(container_spec_cpu_quota" + withMatchers(selector, `container!="",container!="POD"`) + " / container_spec_cpu_period" + withMatchers(selector, `container!="",container!="POD"`) + ")", unit: "percent of configured CPU limit"},
		{name: "traffic rate", query: "sum(rate(http_requests_total" + selector + "[5m]))", unit: "requests/second"},
		{name: "error rate", query: "sum(rate(http_requests_total" + withExtraMatcher(selector, "status", `=~"5.."`) + "[5m]))", unit: "requests/second (5xx)"},
		{name: "p95 latency", query: "histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket" + selector + "[5m])) by (le))", unit: "seconds"},
	}
	for _, q := range queries {
		instantArgs := map[string]any{"query": q.query, "timestamp": now.Format(time.RFC3339), "truncation_limit": 50}
		if err := p.query(ctx, rec, incidentID, target, q.name, "query", q.query, instantArgs, q.unit, now, false); err != nil {
			return err
		}
		args := map[string]any{
			"query":            q.query,
			"start_time":       window["start_time"],
			"end_time":         window["end_time"],
			"step":             window["step"],
			"truncation_limit": 50,
		}
		if err := p.query(ctx, rec, incidentID, target, q.name, "range_query", q.query, args, q.unit, now, false); err != nil {
			return err
		}
	}
	return nil
}

func (p *PrometheusInvestigator) query(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, target, name, tool, promQL string, args map[string]any, unit string, observedAt time.Time, allowEmpty bool) error {
	res, callErr := p.client.CallTool(ctx, tool, args)
	if callErr != nil {
		return rec.AppendEvent(ctx, incidentID, incidents.EventInput{
			Category:   incidents.CategoryObservation,
			Source:     sourcePrometheus,
			Target:     target,
			Actor:      sourcePrometheus,
			Reason:     name + " unavailable: " + callErr.Error(),
			ObservedAt: observedAt,
			Data:       map[string]any{"available": false, "signal": name, "tool": tool, "query": promQL, "unit": unit},
		})
	}
	data := parseToolData(res.Text())
	count, hasResult := resultCount(data)
	if !hasResult || data["status"] == "error" {
		return rec.AppendEvent(ctx, incidentID, incidents.EventInput{
			Category:   incidents.CategoryObservation,
			Source:     sourcePrometheus,
			Target:     target,
			Actor:      sourcePrometheus,
			Reason:     name + " unavailable: invalid Prometheus response",
			ObservedAt: observedAt,
			Data:       map[string]any{"available": false, "signal": name, "tool": tool, "query": promQL, "unit": unit, "window": argsWindow(args)},
		})
	}
	if hasResult && count == 0 && !allowEmpty {
		return rec.AppendEvent(ctx, incidentID, incidents.EventInput{
			Category:   incidents.CategoryObservation,
			Source:     sourcePrometheus,
			Target:     target,
			Actor:      sourcePrometheus,
			Reason:     name + " unavailable: query returned no series",
			ObservedAt: observedAt,
			Data:       map[string]any{"available": false, "signal": name, "tool": tool, "query": promQL, "unit": unit, "resultCount": 0, "window": argsWindow(args)},
		})
	}
	data["available"] = true
	data["signal"] = name
	data["tool"] = tool
	data["query"] = promQL
	data["unit"] = unit
	data["evaluatedAt"] = observedAt.Format(time.RFC3339)
	if at, ok := args["timestamp"]; ok {
		data["sampleTime"] = at
	}
	if hasResult {
		data["resultCount"] = count
	}
	if w := argsWindow(args); w != nil {
		data["window"] = w
	}
	return recordObservation(ctx, rec, incidentID, sourcePrometheus, target, "queried "+name, data)
}

func (p *PrometheusInvestigator) recordAllUnavailable(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, target string, cause error) error {
	names := []string{"active alerts", "cpu usage", "memory usage", "cpu saturation", "traffic rate", "error rate", "p95 latency"}
	now := p.now().UTC()
	for _, name := range names {
		if err := rec.AppendEvent(ctx, incidentID, incidents.EventInput{
			Category:   incidents.CategoryObservation,
			Source:     sourcePrometheus,
			Target:     target,
			Actor:      sourcePrometheus,
			Reason:     name + " unavailable: " + cause.Error(),
			ObservedAt: now,
			Data:       map[string]any{"available": false, "signal": name},
		}); err != nil {
			return err
		}
	}
	return nil
}

func labelSelector(labels map[string]string) (string, error) {
	if len(labels) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if !prometheusLabelName.MatchString(key) {
			return "", fmt.Errorf("invalid Prometheus label name %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	matchers := make([]string, 0, len(keys))
	for _, key := range keys {
		matchers = append(matchers, key+"="+strconv.Quote(labels[key]))
	}
	return "{" + strings.Join(matchers, ",") + "}", nil
}

func withExtraMatcher(selector, key, matcher string) string {
	if selector == "{}" {
		return "{" + key + matcher + "}"
	}
	return strings.TrimSuffix(selector, "}") + "," + key + matcher + "}"
}

func withMatchers(selector, extra string) string {
	if selector == "{}" {
		return "{" + extra + "}"
	}
	return strings.TrimSuffix(selector, "}") + "," + extra + "}"
}

func resultCount(data map[string]any) (int, bool) {
	result, ok := data["result"].(string)
	if !ok {
		return 0, false
	}
	if strings.TrimSpace(result) == "" {
		return 0, true
	}
	return len(strings.Split(strings.TrimSpace(result), "\n")), true
}

func argsWindow(args map[string]any) map[string]any {
	start, startOK := args["start_time"]
	end, endOK := args["end_time"]
	if !startOK || !endOK {
		return nil
	}
	return map[string]any{"start": start, "end": end, "step": args["step"]}
}
