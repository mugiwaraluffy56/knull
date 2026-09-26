package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

func svcFixture() services.Service {
	return services.Service{
		ID:          uuid.New(),
		Key:         "checkout-api",
		DisplayName: "Checkout API",
		Environment: "production",
		Enabled:     true,
	}
}

func TestStatusForState(t *testing.T) {
	cases := map[incidents.State]fleetStatus{
		incidents.StateReceived:         fleetInvestigating,
		incidents.StateInvestigating:    fleetInvestigating,
		incidents.StatePlanning:         fleetInvestigating,
		incidents.StateValidating:       fleetInvestigating,
		incidents.StateAwaitingApproval: fleetAwaitingApproval,
		incidents.StateRemediating:      fleetRemediating,
		incidents.StateVerifying:        fleetVerifying,
		incidents.StateEscalated:        fleetIncident,
		incidents.StateFailed:           fleetIncident,
		incidents.StateDenied:           fleetIncident,
		incidents.StateRecovered:        fleetHealthy,
		incidents.StateClosed:           fleetHealthy,
	}
	for st, want := range cases {
		if got := statusForState(st); got != want {
			t.Errorf("statusForState(%s) = %s, want %s", st, got, want)
		}
	}
}

func TestSortFleetActiveFirst(t *testing.T) {
	rows := []fleetRow{
		{Key: "auth", Environment: "production", ActiveIncidentCount: 0},
		{Key: "checkout", Environment: "production", ActiveIncidentCount: 1},
		{Key: "billing", Environment: "production", ActiveIncidentCount: 0},
	}
	sortFleet(rows)
	if rows[0].Key != "checkout" {
		t.Fatalf("active incident not sorted first: %+v", rows)
	}
	// Remaining healthy ones sorted by key.
	if rows[1].Key != "auth" || rows[2].Key != "billing" {
		t.Fatalf("healthy ordering wrong: %+v", rows)
	}
}

func TestFleetRowSignalsUnavailableByDefault(t *testing.T) {
	row := buildFleetRow(svcFixture(), nil)
	if row.SignalsAvailable {
		t.Fatal("signals should be unavailable until metrics integration lands")
	}
	if row.Status != fleetHealthy {
		t.Fatalf("no active incident should be HEALTHY, got %s", row.Status)
	}
	if row.LatestIncident != nil {
		t.Fatal("no incident expected")
	}
}

func TestFleetRowWithActiveIncident(t *testing.T) {
	inc := incidents.Incident{State: incidents.StateAwaitingApproval, Summary: "mem regression"}
	row := buildFleetRow(svcFixture(), []incidents.Incident{inc})
	if row.Status != fleetAwaitingApproval || row.ActiveIncidentCount != 1 {
		t.Fatalf("active incident row wrong: %+v", row)
	}
	if row.LatestIncident == nil || row.LatestIncident.Summary != "mem regression" {
		t.Fatalf("latest incident summary missing: %+v", row.LatestIncident)
	}
}
