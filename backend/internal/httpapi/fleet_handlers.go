package httpapi

import (
	"context"
	"net/http"
	"sort"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

// fleetIncidentSource is the incident behavior the fleet view needs.
type fleetIncidentSource interface {
	ListActive(ctx context.Context) ([]incidents.Incident, error)
}

// fleetStatus is the health label shown for a service in the fleet view.
type fleetStatus string

const (
	fleetHealthy          fleetStatus = "HEALTHY"
	fleetInvestigating    fleetStatus = "INVESTIGATING"
	fleetAwaitingApproval fleetStatus = "AWAITING_APPROVAL"
	fleetRemediating      fleetStatus = "REMEDIATING"
	fleetVerifying        fleetStatus = "VERIFYING"
	fleetIncident         fleetStatus = "INCIDENT"
)

// activeIncidentSummary is the compact incident info shown on a fleet row.
type activeIncidentSummary struct {
	ID      string          `json:"id"`
	State   incidents.State `json:"state"`
	Summary string          `json:"summary"`
}

// fleetRow is one service's fleet entry.
type fleetRow struct {
	ServiceID           string                 `json:"serviceId"`
	Key                 string                 `json:"key"`
	DisplayName         string                 `json:"displayName"`
	Environment         string                 `json:"environment"`
	Enabled             bool                   `json:"enabled"`
	Status              fleetStatus            `json:"status"`
	ActiveIncidentCount int                    `json:"activeIncidentCount"`
	LatestIncident      *activeIncidentSummary `json:"latestIncident,omitempty"`
	// SignalsAvailable is false until a metrics integration is connected, so the
	// UI shows telemetry as unavailable rather than implying healthy zeros.
	SignalsAvailable bool `json:"signalsAvailable"`
}

// handleFleet returns the fleet view: every service with its current health,
// derived from active incidents. Telemetry signals are marked unavailable until
// the metrics integration lands, so missing data never reads as healthy.
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	if s.services == nil || s.fleetIncidents == nil {
		writeError(w, http.StatusServiceUnavailable, "fleet is not available")
		return
	}

	svcs, err := s.services.List(r.Context())
	if err != nil {
		s.internalError(w, "list services", err)
		return
	}
	active, err := s.fleetIncidents.ListActive(r.Context())
	if err != nil {
		s.internalError(w, "list active incidents", err)
		return
	}

	// Group active incidents by service key + environment.
	type keyEnv struct{ k, e string }
	byService := map[keyEnv][]incidents.Incident{}
	for _, inc := range active {
		ke := keyEnv{inc.ServiceKey, inc.Environment}
		byService[ke] = append(byService[ke], inc)
	}

	rows := make([]fleetRow, 0, len(svcs))
	seen := map[keyEnv]bool{}
	for _, svc := range svcs {
		ke := keyEnv{svc.Key, svc.Environment}
		seen[ke] = true
		rows = append(rows, buildFleetRow(svc, byService[ke]))
	}
	// Include active incidents for services no longer configured, so they are
	// not silently hidden.
	for ke, incs := range byService {
		if seen[ke] {
			continue
		}
		rows = append(rows, buildOrphanRow(ke.k, ke.e, incs))
	}

	sortFleet(rows)
	writeJSON(w, http.StatusOK, map[string]any{"fleet": rows})
}

func buildFleetRow(svc services.Service, active []incidents.Incident) fleetRow {
	row := fleetRow{
		ServiceID:           svc.ID.String(),
		Key:                 svc.Key,
		DisplayName:         svc.DisplayName,
		Environment:         svc.Environment,
		Enabled:             svc.Enabled,
		Status:              fleetHealthy,
		ActiveIncidentCount: len(active),
		SignalsAvailable:    false,
	}
	applyActive(&row, active)
	return row
}

func buildOrphanRow(key, env string, active []incidents.Incident) fleetRow {
	row := fleetRow{
		Key:                 key,
		DisplayName:         key,
		Environment:         env,
		Enabled:             false,
		Status:              fleetHealthy,
		ActiveIncidentCount: len(active),
		SignalsAvailable:    false,
	}
	applyActive(&row, active)
	return row
}

func applyActive(row *fleetRow, active []incidents.Incident) {
	if len(active) == 0 {
		return
	}
	latest := active[0] // ListActive is ordered by updated_at desc
	row.Status = statusForState(latest.State)
	row.LatestIncident = &activeIncidentSummary{
		ID:      latest.ID.String(),
		State:   latest.State,
		Summary: latest.Summary,
	}
}

func statusForState(st incidents.State) fleetStatus {
	switch st {
	case incidents.StateReceived, incidents.StateInvestigating, incidents.StatePlanning, incidents.StateValidating:
		return fleetInvestigating
	case incidents.StateAwaitingApproval:
		return fleetAwaitingApproval
	case incidents.StateRemediating:
		return fleetRemediating
	case incidents.StateVerifying:
		return fleetVerifying
	case incidents.StateEscalated, incidents.StateFailed, incidents.StateDenied:
		return fleetIncident
	default:
		return fleetHealthy
	}
}

// sortFleet puts services with active incidents first, then by environment/key.
func sortFleet(rows []fleetRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aj := rows[i].ActiveIncidentCount > 0, rows[j].ActiveIncidentCount > 0
		if ai != aj {
			return ai
		}
		if rows[i].Environment != rows[j].Environment {
			return rows[i].Environment < rows[j].Environment
		}
		return rows[i].Key < rows[j].Key
	})
}
