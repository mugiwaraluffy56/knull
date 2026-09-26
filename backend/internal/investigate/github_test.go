package investigate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/mcp"
)

func TestGitHubInspectLinksMemoryChangeToMappedRepository(t *testing.T) {
	client := &githubFake{results: map[string]string{
		"list_commits":         `[{"sha":"abc1234"}]`,
		"get_commit":           `{"sha":"abc1234","html_url":"https://github.com/acme/checkout/commit/abc1234","commit":{"message":"Reduce memory for checkout","committer":{"date":"2026-09-26T11:30:00Z"}},"files":[{"filename":"deploy/checkout.yaml","status":"modified","patch":"@@ -1,2 +1,2 @@\n-  memory: 1Gi\n+  memory: 256Mi"}]}`,
		"search_pull_requests": `{"items":[{"number":42,"title":"Tune checkout limits","html_url":"https://github.com/acme/checkout/pull/42","merged_at":"2026-09-26T11:25:00Z","base":{"ref":"production"}},{"number":43,"title":"Staging only","base":{"ref":"staging"}}]}`,
	}}
	rec := &fakeRecorder{}
	inv := NewGitHubInvestigator(client)
	inv.now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	target := target()
	target.GitHubRepo = "acme/checkout"
	target.GitHubRef = "production"

	if err := inv.Inspect(context.Background(), rec, uuid.New(), target); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(client.calls) != 3 || len(rec.events) != 2 {
		t.Fatalf("calls=%d events=%d, want 3 calls and 2 events", len(client.calls), len(rec.events))
	}
	for _, call := range client.calls {
		if call.args["owner"] != "acme" || call.args["repo"] != "checkout" {
			t.Errorf("call escaped mapped repo: %#v", call.args)
		}
	}
	if client.calls[0].args["sha"] != "production" {
		t.Fatalf("commits not scoped to mapped ref: %#v", client.calls[0].args)
	}
	files, ok := rec.events[0].Data["files"].([]map[string]any)
	if !ok || len(files) != 1 {
		t.Fatalf("missing manifest change: %#v", rec.events[0].Data)
	}
	lines := files[0]["relevantLines"].([]string)
	if len(lines) != 2 || lines[0] != "- memory: 1Gi" || lines[1] != "+ memory: 256Mi" {
		t.Fatalf("memory change not linked: %#v", lines)
	}
	prs := rec.events[1].Data["pullRequests"].([]map[string]any)
	if len(prs) != 1 || prs[0]["number"] != float64(42) {
		t.Fatalf("PR results crossed environment ref: %#v", prs)
	}
}

func TestGitHubMissingMappingAndAccessFailureAreUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target Target
		fail   string
	}{
		{name: "no mapping", target: target()},
		{name: "denied", target: Target{ServiceKey: "checkout-api", Environment: "production", GitHubRepo: "acme/checkout", GitHubRef: "production"}, fail: "list_commits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &githubFake{fail: tc.fail}
			rec := &fakeRecorder{}
			if err := NewGitHubInvestigator(client).Inspect(context.Background(), rec, uuid.New(), tc.target); err != nil {
				t.Fatal(err)
			}
			if len(rec.events) != 1 || rec.events[0].Data["available"] != false {
				t.Fatalf("failure was not visible: %#v", rec.events)
			}
			if tc.fail == "" && len(client.calls) > 0 {
				t.Fatalf("unmapped repository was queried: %#v", client.calls)
			}
		})
	}
}

func TestGitHubResponseRejectsUnparseableCommitList(t *testing.T) {
	client := &githubFake{results: map[string]string{"list_commits": `{"not_commits":[]}`}}
	rec := &fakeRecorder{}
	target := target()
	target.GitHubRepo, target.GitHubRef = "acme/checkout", "production"
	if err := NewGitHubInvestigator(client).Inspect(context.Background(), rec, uuid.New(), target); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 1 || !strings.Contains(rec.events[0].Reason, "unavailable") {
		t.Fatalf("invalid response was treated as a finding: %#v", rec.events)
	}
}

type githubFake struct {
	results map[string]string
	fail    string
	calls   []recordedCall
}

func (f *githubFake) CallTool(_ context.Context, name string, args map[string]any) (mcp.ToolResult, error) {
	f.calls = append(f.calls, recordedCall{name: name, args: args})
	if name == f.fail {
		return mcp.ToolResult{}, errors.New("GitHub access denied")
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: f.results[name]}}}, nil
}
