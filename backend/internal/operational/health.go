package operational

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
)

type Dependencies interface {
	PingPostgres(context.Context) error
	PingRedis(context.Context) error
}
type Incidents interface {
	List(context.Context) ([]incidents.Incident, error)
}
type Sandbox interface {
	ClusterUID(context.Context) (string, error)
}
type Endpoint struct{ URL, Token string }
type State struct {
	Status            string      `json:"status"`
	LastSuccess       *time.Time  `json:"lastSuccess,omitempty"`
	LastError         string      `json:"lastError,omitempty"`
	AffectedIncidents []uuid.UUID `json:"affectedIncidents,omitempty"`
}

// Monitor performs bounded probes on demand, retaining last known status in
// memory. Error strings never contain raw URLs, tokens or response bodies.
type Monitor struct {
	Deps               Dependencies
	Incidents          Incidents
	Sandbox            Sandbox
	ExpectedSandboxUID string
	Endpoints          map[string]Endpoint
	Client             *http.Client
	mu                 sync.Mutex
	last               map[string]State
}

func (m *Monitor) Check(ctx context.Context) map[string]State {
	if m == nil {
		return map[string]State{}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	results := map[string]State{}
	if m.Deps != nil {
		results["postgres"] = m.state("postgres", m.Deps.PingPostgres(ctx), true)
		results["redis"] = m.state("redis", m.Deps.PingRedis(ctx), true)
	}
	for name, endpoint := range m.Endpoints {
		results[name] = m.state(name, m.probe(ctx, endpoint), endpoint.URL != "")
	}
	if m.Sandbox != nil && m.ExpectedSandboxUID != "" {
		uid, err := m.Sandbox.ClusterUID(ctx)
		if err == nil && uid != m.ExpectedSandboxUID {
			err = errors.New("cluster identity mismatch")
		}
		results["sandbox"] = m.state("sandbox", err, true)
	} else {
		results["sandbox"] = m.state("sandbox", nil, false)
	}
	var active []uuid.UUID
	if m.Incidents != nil {
		list, err := m.Incidents.List(ctx)
		if err == nil {
			for _, inc := range list {
				if !inc.State.IsTerminal() {
					active = append(active, inc.ID)
				}
			}
		}
	}
	for name, state := range results {
		if state.Status == "unavailable" {
			state.AffectedIncidents = active
			results[name] = state
		}
	}
	return results
}

func (m *Monitor) state(name string, err error, configured bool) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		m.last = map[string]State{}
	}
	state := m.last[name]
	if !configured {
		state.Status, state.LastError = "disabled", ""
	} else if err != nil {
		state.Status, state.LastError = "unavailable", "probe failed"
	} else {
		now := time.Now().UTC()
		state.Status, state.LastSuccess, state.LastError = "ok", &now, ""
	}
	m.last[name] = state
	return state
}

func (m *Monitor) probe(ctx context.Context, endpoint Endpoint) error {
	if endpoint.URL == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.URL, nil)
	if err != nil {
		return err
	}
	if endpoint.Token != "" {
		request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	}
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode < 200 || (response.StatusCode >= 300 && response.StatusCode != http.StatusMethodNotAllowed) {
		return errors.New("provider rejected health probe")
	}
	return nil
}
