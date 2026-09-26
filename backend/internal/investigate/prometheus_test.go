package investigate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/mcp"
)

func TestPrometheusInspectQueriesMappedSignalsAndRecordsWindows(t *testing.T) {
	calls := &recordingCaller{result: `{"result":"{app=\"checkout-api\"} => 0 @[1790424000]","warnings":[]}`}
	rec := &fakeRecorder{}
	inv := NewPrometheusInvestigator(calls)
	inv.now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }

	target := target()
	target.PrometheusLabels = map[string]string{"environment": "production", "app": "checkout-api"}
	if err := inv.Inspect(context.Background(), rec, uuid.New(), target); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(calls.calls) != 13 {
		t.Fatalf("made %d calls, want alert query + 6 instant/range pairs", len(calls.calls))
	}
	if len(rec.events) != 13 {
		t.Fatalf("recorded %d events, want one per query", len(rec.events))
	}
	for _, call := range calls.calls {
		query, _ := call.args["query"].(string)
		if !strings.Contains(query, `app="checkout-api"`) || !strings.Contains(query, `environment="production"`) {
			t.Errorf("query is not scoped to service and environment labels: %s", query)
		}
		if call.name == "range_query" {
			if call.args["start_time"] == nil || call.args["end_time"] == nil || call.args["step"] != "1m" {
				t.Errorf("range query missing time bounds: %#v", call.args)
			}
		}
	}
	for _, event := range rec.events {
		if event.Source != sourcePrometheus || event.Target != "checkout-api/production" || event.Category != incidents.CategoryObservation {
			t.Errorf("wrong event metadata: %+v", event)
		}
		if event.Data["available"] != true {
			t.Errorf("zero-valued result was not retained as available: %#v", event.Data)
		}
		if event.Data["query"] == "" || event.Data["unit"] == "" {
			t.Errorf("event missing query or unit metadata: %#v", event.Data)
		}
	}
}

func TestPrometheusEmptyMetricSeriesIsUnavailableButNoAlertsIsAvailable(t *testing.T) {
	calls := &recordingCaller{result: `{"result":"","warnings":[]}`}
	rec := &fakeRecorder{}
	inv := NewPrometheusInvestigator(calls)
	target := target()
	target.PrometheusLabels = map[string]string{"app": "checkout-api", "environment": "production"}

	if err := inv.Inspect(context.Background(), rec, uuid.New(), target); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(rec.events) != 13 {
		t.Fatalf("recorded %d events, want 13", len(rec.events))
	}
	if rec.events[0].Data["available"] != true || rec.events[0].Data["resultCount"] != 0 {
		t.Fatalf("empty active-alert vector should mean no active alerts: %#v", rec.events[0].Data)
	}
	for _, event := range rec.events[1:] {
		if event.Data["available"] != false || event.Data["resultCount"] != 0 {
			t.Errorf("empty metric vector must be unavailable, not zero: %#v", event.Data)
		}
		if !strings.Contains(event.Reason, "no series") {
			t.Errorf("missing-series reason not shown: %q", event.Reason)
		}
	}
}

func TestPrometheusMissingOrInvalidSelectorDoesNotQuery(t *testing.T) {
	for name, labels := range map[string]map[string]string{
		"missing":           {},
		"invalid":           {`app"} or vector(1) #`: "x"},
		"wrong environment": {"app": "checkout-api", "environment": "staging"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := &recordingCaller{}
			rec := &fakeRecorder{}
			inv := NewPrometheusInvestigator(calls)
			target := target()
			target.PrometheusLabels = labels
			if err := inv.Inspect(context.Background(), rec, uuid.New(), target); err != nil {
				t.Fatalf("inspect: %v", err)
			}
			if len(calls.calls) != 0 {
				t.Fatalf("unscoped Prometheus queries made: %v", calls.calls)
			}
			if len(rec.events) != 7 {
				t.Fatalf("recorded %d unavailable signals, want 7", len(rec.events))
			}
			for _, event := range rec.events {
				if event.Data["available"] != false {
					t.Errorf("signal not marked unavailable: %#v", event.Data)
				}
			}
		})
	}
}

func TestLabelSelectorEscapesValuesAndSortsKeys(t *testing.T) {
	selector, err := labelSelector(map[string]string{"z": `v"x`, "app": "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	if selector != `{app="checkout",z="v\"x"}` {
		t.Fatalf("unexpected selector %q", selector)
	}
}

type recordedCall struct {
	name string
	args map[string]any
}

type recordingCaller struct {
	calls  []recordedCall
	result string
}

func (c *recordingCaller) CallTool(_ context.Context, name string, args map[string]any) (mcp.ToolResult, error) {
	c.calls = append(c.calls, recordedCall{name: name, args: args})
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: c.result}}}, nil
}
