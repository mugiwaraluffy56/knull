// Package baddeployment correlates read-only rollout evidence and proposes an
// exact rollback. It has no sandbox or production mutation capability.
package baddeployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
)

var ErrNoCorrelation = errors.New("bad deployment correlation unavailable")

var pinnedImage = regexp.MustCompile(`^([a-z0-9][a-z0-9./:_-]+)@(sha256:[a-f0-9]{64})$`)
var imageChange = regexp.MustCompile(`^([+-]) image: ([a-z0-9][a-z0-9./:_-]+@sha256:[a-f0-9]{64})$`)
var commitSHA = regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`)
var resourceVersion = regexp.MustCompile(`^[1-9][0-9]*$`)

type IncidentStore interface {
	Get(context.Context, uuid.UUID) (incidents.Incident, error)
	Events(context.Context, uuid.UUID) ([]incidents.Event, error)
	AppendEventID(context.Context, uuid.UUID, incidents.EventInput) (uuid.UUID, error)
}

type ServiceStore interface {
	Get(context.Context, uuid.UUID) (services.Service, error)
}
type ActionPlanner interface {
	Create(context.Context, uuid.UUID, actions.Draft) (actions.Created, error)
}

type Service struct {
	incidents IncidentStore
	services  ServiceStore
	actions   ActionPlanner
}

func NewService(inc IncidentStore, svc ServiceStore, planner ActionPlanner) *Service {
	return &Service{incidents: inc, services: svc, actions: planner}
}

type Finding struct {
	CurrentImage    string   `json:"currentImage"`
	PreviousImage   string   `json:"previousImage"`
	CurrentDigest   string   `json:"currentDigest"`
	PreviousDigest  string   `json:"previousDigest"`
	Container       string   `json:"container"`
	ResourceUID     string   `json:"resourceUid"`
	ResourceVersion string   `json:"resourceVersion"`
	CommitSHA       string   `json:"commitSha"`
	CommitURL       string   `json:"commitUrl"`
	EvidenceIDs     []string `json:"evidenceIds"`
}

type Proposed struct {
	CorrelationEventID uuid.UUID       `json:"correlationEventId"`
	Finding            Finding         `json:"finding"`
	Action             actions.Created `json:"action"`
}

// Propose requires matching CrashLoopBackOff pods, Kubernetes BackOff events,
// a pinned live deployment image, and a GitHub diff replacing the previous
// pinned image with that exact current image on the configured repository/ref.
// No missing or ambiguous evidence is filled in by inference.
func (s *Service) Propose(ctx context.Context, incidentID uuid.UUID) (Proposed, error) {
	if s == nil || s.incidents == nil || s.services == nil || s.actions == nil {
		return Proposed{}, ErrNoCorrelation
	}
	inc, err := s.incidents.Get(ctx, incidentID)
	if err != nil {
		return Proposed{}, err
	}
	if inc.State != incidents.StatePlanning || inc.ServiceID == nil {
		return Proposed{}, ErrNoCorrelation
	}
	svc, err := s.services.Get(ctx, *inc.ServiceID)
	if err != nil {
		return Proposed{}, err
	}
	if !svc.Enabled || svc.Key != inc.ServiceKey || svc.Environment != inc.Environment || svc.GitHubRepo == "" || svc.GitHubRef == "" {
		return Proposed{}, ErrNoCorrelation
	}
	events, err := s.incidents.Events(ctx, incidentID)
	if err != nil {
		return Proposed{}, err
	}
	finding, err := Correlate(events, svc)
	if err != nil {
		return Proposed{}, err
	}
	correlationID, err := s.incidents.AppendEventID(ctx, incidentID, incidents.EventInput{Category: incidents.CategoryHypothesis, Source: "bad-deployment-correlation", Actor: "knull", Target: svc.K8sCluster + "/" + svc.K8sNamespace + "/" + svc.K8sWorkload, Reason: "CrashLoopBackOff matches a recent pinned image change on the configured GitHub ref", Data: map[string]any{"finding": finding, "evidenceIds": finding.EvidenceIDs, "productionEligible": false}})
	if err != nil {
		return Proposed{}, err
	}
	draft := actions.Draft{Type: actions.Rollback, Environment: svc.Environment, Target: actions.Target{Cluster: svc.K8sCluster, Namespace: svc.K8sNamespace, Kind: "Deployment", Name: svc.K8sWorkload, Container: finding.Container}, Field: "imageDigest", CurrentValue: finding.CurrentDigest, DesiredValue: finding.PreviousDigest, Preconditions: actions.Preconditions{ResourceUID: finding.ResourceUID, ResourceVersion: finding.ResourceVersion, CurrentValue: finding.CurrentDigest}, ExpectedImpact: "Roll deployment pods back to the previous pinned image; a rolling replacement is expected", Risk: "HIGH", EvidenceIDs: finding.EvidenceIDs}
	created, err := s.actions.Create(ctx, incidentID, draft)
	if err != nil {
		return Proposed{CorrelationEventID: correlationID, Finding: finding}, err
	}
	return Proposed{CorrelationEventID: correlationID, Finding: finding, Action: created}, nil
}

// Correlate is deterministic and read-only; it never proposes a tag or an
// image whose digest differs from the live deployment evidence.
func Correlate(events []incidents.Event, svc services.Service) (Finding, error) {
	k8sTarget := svc.K8sCluster + "/" + svc.K8sNamespace + "/" + svc.K8sWorkload
	githubTarget := svc.GitHubRepo + "@" + svc.GitHubRef + " (" + svc.Environment + ")"
	var deployment *incidents.Event
	var pods *incidents.Event
	var kubeEvent *incidents.Event
	var logs *incidents.Event
	for i := range events {
		e := &events[i]
		if e.Source != "kubernetes" || e.Category != incidents.CategoryObservation || e.Data["available"] == false || e.Target != k8sTarget {
			continue
		}
		switch e.Data["tool"] {
		case "get_deployment":
			deployment = e
		case "list_pods":
			pods = e
		case "get_events":
			kubeEvent = e
		case "get_logs":
			logs = e
		}
	}
	if deployment == nil || pods == nil || kubeEvent == nil || !hasCrashLoop(*pods, svc.K8sWorkload) || !hasBackOff(*kubeEvent, svc.K8sWorkload) {
		return Finding{}, ErrNoCorrelation
	}
	uid, version, container, currentImage := liveDeployment(*deployment)
	match := pinnedImage.FindStringSubmatch(currentImage)
	if uid == "" || version == "" || container == "" || len(match) != 3 {
		return Finding{}, ErrNoCorrelation
	}
	if _, err := uuid.Parse(uid); err != nil {
		return Finding{}, ErrNoCorrelation
	}
	if !resourceVersion.MatchString(version) {
		return Finding{}, ErrNoCorrelation
	}
	var best *incidents.Event
	var previousImage string
	for i := range events {
		e := &events[i]
		if e.Source != "github" || e.Category != incidents.CategoryObservation || e.Data["available"] == false || e.Target != githubTarget || e.Data["repo"] != svc.GitHubRepo || e.Data["ref"] != svc.GitHubRef {
			continue
		}
		sha := asString(e.Data["sha"])
		url := asString(e.Data["url"])
		if !commitSHA.MatchString(sha) || url != "https://github.com/"+svc.GitHubRepo+"/commit/"+sha {
			continue
		}
		old, ok := githubImageChange(*e, currentImage)
		if !ok {
			continue
		}
		if !e.ObservedAt.IsZero() && !pods.ObservedAt.IsZero() && (e.ObservedAt.After(pods.ObservedAt.Add(5*time.Minute)) || pods.ObservedAt.Sub(e.ObservedAt) > 7*24*time.Hour) {
			continue
		}
		if best == nil || e.ObservedAt.After(best.ObservedAt) {
			best = e
			previousImage = old
		}
	}
	if best == nil {
		return Finding{}, ErrNoCorrelation
	}
	oldMatch := pinnedImage.FindStringSubmatch(previousImage)
	if len(oldMatch) != 3 || oldMatch[1] != match[1] || oldMatch[2] == match[2] {
		return Finding{}, ErrNoCorrelation
	}
	refs := []string{deployment.ID.String(), pods.ID.String(), kubeEvent.ID.String(), best.ID.String()}
	if logs != nil {
		refs = append(refs, logs.ID.String())
	}
	for _, id := range refs {
		if id == uuid.Nil.String() {
			return Finding{}, ErrNoCorrelation
		}
	}
	return Finding{CurrentImage: currentImage, PreviousImage: previousImage, CurrentDigest: match[2], PreviousDigest: oldMatch[2], Container: container, ResourceUID: uid, ResourceVersion: version, CommitSHA: asString(best.Data["sha"]), CommitURL: asString(best.Data["url"]), EvidenceIDs: refs}, nil
}

func liveDeployment(e incidents.Event) (uid, version, container, image string) {
	data := e.Data
	if nested, ok := data["deployment"].(map[string]any); ok {
		data = nested
	}
	metadata, _ := data["metadata"].(map[string]any)
	uid, version = asString(metadata["uid"]), asString(metadata["resourceVersion"])
	spec, _ := data["spec"].(map[string]any)
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	if len(containers) != 1 {
		return uid, version, "", ""
	}
	item, _ := containers[0].(map[string]any)
	return uid, version, asString(item["name"]), asString(item["image"])
}

func hasCrashLoop(e incidents.Event, workload string) bool {
	pods, _ := e.Data["pods"].([]any)
	for _, raw := range pods {
		pod, _ := raw.(map[string]any)
		if !strings.HasPrefix(asString(pod["name"]), workload+"-") {
			continue
		}
		if asString(pod["phase"]) == "CrashLoopBackOff" {
			return true
		}
		status, _ := pod["status"].(map[string]any)
		states, _ := status["containerStatuses"].([]any)
		for _, rawState := range states {
			entry, _ := rawState.(map[string]any)
			state, _ := entry["state"].(map[string]any)
			waiting, _ := state["waiting"].(map[string]any)
			if asString(waiting["reason"]) == "CrashLoopBackOff" {
				return true
			}
		}
	}
	return false
}

func hasBackOff(e incidents.Event, workload string) bool {
	events, _ := e.Data["events"].([]any)
	for _, raw := range events {
		item, _ := raw.(map[string]any)
		object, _ := item["involvedObject"].(map[string]any)
		name := asString(object["name"])
		if name != "" && name != workload && !strings.HasPrefix(name, workload+"-") {
			continue
		}
		if strings.EqualFold(asString(item["reason"]), "BackOff") || strings.Contains(asString(item["message"]), "Back-off restarting failed container") {
			return true
		}
	}
	return false
}

func githubImageChange(e incidents.Event, currentImage string) (string, bool) {
	files, _ := e.Data["files"].([]any)
	for _, raw := range files {
		file, _ := raw.(map[string]any)
		lines, _ := file["relevantLines"].([]any)
		var old, new string
		oldCount, newCount := 0, 0
		for _, rawLine := range lines {
			line := asString(rawLine)
			match := imageChange.FindStringSubmatch(line)
			if len(match) != 3 {
				continue
			}
			if match[1] == "-" {
				old = match[2]
				oldCount++
			} else {
				new = match[2]
				newCount++
			}
		}
		if new == currentImage && oldCount == 1 && newCount == 1 {
			return old, true
		}
	}
	return "", false
}

func asString(value any) string { text, _ := value.(string); return text }

// DecodeFinding permits downstream validation to reuse the persisted exact
// full image references without trusting free-form incident notes.
func DecodeFinding(event incidents.Event) (Finding, error) {
	if event.Source != "bad-deployment-correlation" || event.Category != incidents.CategoryHypothesis {
		return Finding{}, ErrNoCorrelation
	}
	body, err := json.Marshal(event.Data["finding"])
	if err != nil {
		return Finding{}, err
	}
	var finding Finding
	if err := json.Unmarshal(body, &finding); err != nil {
		return Finding{}, err
	}
	current := pinnedImage.FindStringSubmatch(finding.CurrentImage)
	previous := pinnedImage.FindStringSubmatch(finding.PreviousImage)
	if len(current) != 3 || len(previous) != 3 || current[1] != previous[1] || current[2] != finding.CurrentDigest || previous[2] != finding.PreviousDigest || len(finding.EvidenceIDs) < 4 {
		return Finding{}, fmt.Errorf("%w: incomplete finding", ErrNoCorrelation)
	}
	for _, reference := range finding.EvidenceIDs {
		if _, err := uuid.Parse(reference); err != nil {
			return Finding{}, fmt.Errorf("%w: invalid evidence reference", ErrNoCorrelation)
		}
	}
	return finding, nil
}
