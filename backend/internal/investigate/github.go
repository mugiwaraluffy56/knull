package investigate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

// GitHubInvestigator reads recent changes in the repository and ref mapped to
// an incident's service. It has no write tools or repository selection of its
// own; both owner and repo come only from the configured mapping.
type GitHubInvestigator struct {
	client toolCaller
	now    func() time.Time
}

func NewGitHubInvestigator(client toolCaller) *GitHubInvestigator {
	return &GitHubInvestigator{client: client, now: time.Now}
}

const sourceGitHub = "github"

var githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var githubSHAPattern = regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`)
var githubConfigChange = regexp.MustCompile(`^([+-])\s*(memory|cpu|image|replicas?)\s*:\s*["']?([A-Za-z0-9./:_@-]+)`)

func (g *GitHubInvestigator) Inspect(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, t Target) error {
	target := t.GitHubRepo + "@" + t.GitHubRef + " (" + t.Environment + ")"
	if !githubRepoPattern.MatchString(t.GitHubRepo) || t.GitHubRef == "" {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("GitHub repository and ref are not configured"))
	}
	owner, repo, _ := strings.Cut(t.GitHubRepo, "/")
	now := g.now().UTC()
	since := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	args := map[string]any{
		"owner": owner, "repo": repo, "sha": t.GitHubRef,
		"since": since, "perPage": 10, "page": 1,
		"fields": []string{"sha", "html_url", "commit"},
	}
	result, err := g.client.CallTool(ctx, "list_commits", args)
	if err != nil {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("list_commits: %w", err))
	}
	commits, err := githubItems(result.Text(), "commits")
	if err != nil {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("list_commits: %w", err))
	}
	if len(commits) == 0 {
		if err := rec.AppendEvent(ctx, incidentID, incidents.EventInput{
			Category: incidents.CategoryObservation,
			Source:   sourceGitHub,
			Target:   target,
			Actor:    sourceGitHub,
			Reason:   "no commits found in the last seven days on mapped ref",
			Data:     map[string]any{"available": true, "repo": t.GitHubRepo, "ref": t.GitHubRef, "since": since, "count": 0},
		}); err != nil {
			return err
		}
	}
	for index, commit := range commits {
		if index >= 5 {
			break
		}
		sha := stringField(commit, "sha")
		if !githubSHAPattern.MatchString(sha) {
			continue
		}
		if err := g.inspectCommit(ctx, rec, incidentID, t, owner, repo, sha); err != nil {
			return err
		}
	}
	return g.inspectPullRequests(ctx, rec, incidentID, t, owner, repo, since)
}

func (g *GitHubInvestigator) inspectCommit(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, t Target, owner, repo, sha string) error {
	target := t.GitHubRepo + "@" + t.GitHubRef + " (" + t.Environment + ")"
	result, err := g.client.CallTool(ctx, "get_commit", map[string]any{
		"owner": owner, "repo": repo, "sha": sha,
		"detail": "full_patch", "perPage": 100, "page": 1,
	})
	if err != nil {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("get_commit %s: %w", sha, err))
	}
	commit, err := githubObject(result.Text())
	if err != nil {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("get_commit %s: %w", sha, err))
	}
	changes := relevantGitHubFiles(commit["files"])
	url := fmt.Sprintf("https://github.com/%s/commit/%s", t.GitHubRepo, sha)
	commitData, _ := commit["commit"].(map[string]any)
	message := firstLine(stringField(commitData, "message"))
	date := githubCommitDate(commitData)
	return rec.AppendEvent(ctx, incidentID, incidents.EventInput{
		Category:   incidents.CategoryObservation,
		Source:     sourceGitHub,
		Target:     target,
		Actor:      sourceGitHub,
		Reason:     "inspected commit " + sha,
		ObservedAt: date,
		Data: map[string]any{
			"available": true, "repo": t.GitHubRepo, "ref": t.GitHubRef,
			"sha": sha, "url": url, "message": message, "files": changes,
			"correlation": "possible deployment change; verify against rollout and workload evidence",
		},
	})
}

func (g *GitHubInvestigator) inspectPullRequests(ctx context.Context, rec EventRecorder, incidentID uuid.UUID, t Target, owner, repo, since string) error {
	target := t.GitHubRepo + "@" + t.GitHubRef + " (" + t.Environment + ")"
	result, err := g.client.CallTool(ctx, "search_pull_requests", map[string]any{
		"owner": owner, "repo": repo, "query": "repo:" + t.GitHubRepo + " is:pr is:merged updated:>=" + since[:10],
		"perPage": 10, "page": 1,
		"fields": []string{"number", "title", "html_url", "merged_at", "base"},
	})
	if err != nil {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("search_pull_requests: %w", err))
	}
	prs, err := githubItems(result.Text(), "items")
	if err != nil {
		return recordUnavailable(ctx, rec, incidentID, sourceGitHub, target, fmt.Errorf("search_pull_requests: %w", err))
	}
	items := make([]map[string]any, 0, len(prs))
	for _, pr := range prs {
		base, _ := pr["base"].(map[string]any)
		if stringField(base, "ref") != t.GitHubRef {
			continue
		}
		number, ok := pr["number"].(float64)
		if !ok || number <= 0 || number != float64(int(number)) {
			continue
		}
		items = append(items, map[string]any{
			"number": pr["number"], "title": firstLine(stringField(pr, "title")),
			"url": fmt.Sprintf("https://github.com/%s/pull/%d", t.GitHubRepo, int(number)), "mergedAt": stringField(pr, "merged_at"),
		})
	}
	return rec.AppendEvent(ctx, incidentID, incidents.EventInput{
		Category: incidents.CategoryObservation,
		Source:   sourceGitHub,
		Target:   target,
		Actor:    sourceGitHub,
		Reason:   fmt.Sprintf("found %d recent merged pull requests on mapped ref", len(items)),
		Data:     map[string]any{"available": true, "repo": t.GitHubRepo, "ref": t.GitHubRef, "pullRequests": items},
	})
}

func githubItems(text, field string) ([]map[string]any, error) {
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("decode GitHub response: %w", err)
	}
	if object, ok := raw.(map[string]any); ok {
		raw = object[field]
	}
	array, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub response has no %s list", field)
	}
	items := make([]map[string]any, 0, len(array))
	for _, item := range array {
		object, ok := item.(map[string]any)
		if ok {
			items = append(items, object)
		}
	}
	return items, nil
}

func githubObject(text string) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil || object == nil {
		return nil, fmt.Errorf("invalid GitHub object response")
	}
	return object, nil
}

func relevantGitHubFiles(raw any) []map[string]any {
	files, _ := raw.([]any)
	changes := make([]map[string]any, 0, len(files))
	for _, rawFile := range files {
		file, ok := rawFile.(map[string]any)
		if !ok {
			continue
		}
		name := stringField(file, "filename")
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".json") && !strings.HasSuffix(lower, ".tf") && !strings.Contains(lower, "helm") && !strings.Contains(lower, "deploy") {
			continue
		}
		lines := make([]string, 0, 8)
		for _, line := range strings.Split(stringField(file, "patch"), "\n") {
			if len(lines) >= 8 {
				break
			}
			if match := githubConfigChange.FindStringSubmatch(line); len(match) == 4 {
				lines = append(lines, match[1]+" "+match[2]+": "+match[3])
			}
		}
		changes = append(changes, map[string]any{"path": name, "status": file["status"], "relevantLines": lines})
		if len(changes) >= 20 {
			break
		}
	}
	return changes
}

func githubCommitDate(commit map[string]any) time.Time {
	committer, _ := commit["committer"].(map[string]any)
	date, _ := time.Parse(time.RFC3339, stringField(committer, "date"))
	return date
}

func stringField(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	if len(line) > 200 {
		return line[:200]
	}
	return line
}
