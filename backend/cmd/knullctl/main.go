// knullctl reads the same authenticated incident API used by the web UI.
// It has no Kubernetes credentials or direct production mutation command.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type client struct {
	base   string
	cookie string
	http   *http.Client
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "knullctl:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("knullctl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonMode := flags.Bool("json", false, "print API JSON")
	base := flags.String("api", envOr("KNULL_API_URL", "http://localhost:8080"), "Knull API base URL")
	if err := flags.Parse(args); err != nil {
		return usage()
	}
	parts := flags.Args()
	if len(parts) == 0 {
		return usage()
	}
	parsed, err := url.Parse(*base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("--api must be an http(s) base URL without credentials or query")
	}
	if parsed.Scheme == "http" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return errors.New("--api must use HTTPS outside localhost")
	}
	c := client{base: strings.TrimRight(parsed.String(), "/"), cookie: os.Getenv("KNULL_SESSION_COOKIE"), http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("API redirect refused") }}}
	ctx := context.Background()
	var method, path string
	var body any
	switch parts[0] {
	case "check":
		if len(parts) != 1 {
			return usage()
		}
		method, path = http.MethodGet, "/readyz"
	case "services":
		if len(parts) != 1 {
			return usage()
		}
		method, path = http.MethodGet, "/api/services"
	case "integrations":
		if len(parts) != 1 {
			return usage()
		}
		method, path = http.MethodGet, "/api/integrations/credentials"
	case "health":
		if len(parts) != 1 {
			return usage()
		}
		method, path = http.MethodGet, "/api/integrations/health"
	case "incidents":
		if len(parts) != 1 {
			return usage()
		}
		method, path = http.MethodGet, "/api/incidents"
	case "incident", "events":
		if len(parts) != 2 {
			return usage()
		}
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return errors.New("incident ID must be a UUID")
		}
		method, path = http.MethodGet, "/api/incidents/"+id.String()
		if parts[0] == "events" {
			path += "/events"
		}
	case "workflow":
		if len(parts) != 2 {
			return usage()
		}
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return errors.New("incident ID must be a UUID")
		}
		if c.cookie == "" {
			return errors.New("KNULL_SESSION_COOKIE is required; sign in through the web UI and supply its session cookie")
		}
		incident, err := c.request(ctx, http.MethodGet, "/api/incidents/"+id.String(), nil)
		if err != nil {
			return err
		}
		events, err := c.request(ctx, http.MethodGet, "/api/incidents/"+id.String()+"/events", nil)
		if err != nil {
			return err
		}
		return printWorkflow(out, *jsonMode, incident, events)
	case "start":
		if len(parts) < 3 {
			return errors.New("usage: knullctl start SERVICE_UUID SUMMARY")
		}
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return errors.New("service ID must be a UUID")
		}
		method, path = http.MethodPost, "/api/incidents"
		body = map[string]any{"serviceId": id.String(), "summary": strings.Join(parts[2:], " "), "symptoms": map[string]string{}}
	default:
		return usage()
	}
	if path != "/readyz" && c.cookie == "" {
		return errors.New("KNULL_SESSION_COOKIE is required; sign in through the web UI and supply its session cookie")
	}
	result, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	if *jsonMode {
		_, err = fmt.Fprintln(out, string(result))
		return err
	}
	return printHuman(out, parts[0], result)
}

func (c client) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, input)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API unavailable: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var apiError struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &apiError)
		if apiError.Error == "" {
			apiError.Error = response.Status
		}
		return nil, fmt.Errorf("API %s: %s", response.Status, apiError.Error)
	}
	return data, nil
}

func printHuman(out io.Writer, command string, data []byte) error {
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		_, err = fmt.Fprintln(out, strings.TrimSpace(string(data)))
		return err
	}
	if command == "check" {
		_, err := fmt.Fprintln(out, "API ready")
		return err
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(pretty))
	return err
}

type workflowIncident struct {
	ID                string `json:"id"`
	ServiceKey        string `json:"serviceKey"`
	Environment       string `json:"environment"`
	Summary           string `json:"summary"`
	State             string `json:"state"`
	WorkflowSessionID string `json:"workflowSessionId"`
	WorkflowRunID     string `json:"workflowRunId"`
}

type workflowEvent struct {
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Category  string         `json:"category"`
	Source    string         `json:"source"`
	Reason    string         `json:"reason"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"createdAt"`
}

func printWorkflow(out io.Writer, jsonMode bool, incidentData, eventData []byte) error {
	var incident workflowIncident
	if err := json.Unmarshal(incidentData, &incident); err != nil {
		return fmt.Errorf("decode incident response: %w", err)
	}
	var eventEnvelope struct {
		Events []workflowEvent `json:"events"`
	}
	if err := json.Unmarshal(eventData, &eventEnvelope); err != nil {
		return fmt.Errorf("decode incident events response: %w", err)
	}
	if jsonMode {
		var rawIncident, rawEvents json.RawMessage
		if err := json.Unmarshal(incidentData, &rawIncident); err != nil {
			return err
		}
		if err := json.Unmarshal(eventData, &rawEvents); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Incident json.RawMessage `json:"incident"`
			Events   json.RawMessage `json:"events"`
		}{rawIncident, rawEvents})
	}

	if _, err := fmt.Fprintf(out, "Incident %s\nState: %s\nService: %s (%s)\nSummary: %s\n", incident.ID, valueOr(incident.State, "unknown"), valueOr(incident.ServiceKey, "unmapped"), valueOr(incident.Environment, "unknown"), valueOr(incident.Summary, "(none)")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Workflow session: %s\nWorkflow run: %s\nProgress events: %d\n", valueOr(incident.WorkflowSessionID, "not started"), valueOr(incident.WorkflowRunID, "not started"), len(eventEnvelope.Events)); err != nil {
		return err
	}
	for _, event := range eventEnvelope.Events {
		detail := event.Reason
		if trueforgeType, ok := event.Data["trueforgeType"].(string); ok && trueforgeType != "" {
			detail = trueforgeType + ": " + detail
		}
		label := event.Category
		if label == "" {
			label = event.Type
		}
		if _, err := fmt.Fprintf(out, "  #%d %s %s — %s\n", event.Seq, event.CreatedAt.Format(time.RFC3339), valueOr(label, "event"), valueOr(detail, "(no details)")); err != nil {
			return err
		}
	}
	return nil
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func usage() error {
	return errors.New("usage: knullctl [--api URL] [--json] check|health|services|integrations|incidents|incident UUID|events UUID|workflow UUID|start SERVICE_UUID SUMMARY")
}
