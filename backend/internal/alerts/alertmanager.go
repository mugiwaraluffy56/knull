// Package alerts normalizes incoming Prometheus Alertmanager webhook
// notifications into the provider-neutral incident intake contract and
// deduplicates repeated deliveries onto a single active incident.
package alerts

import (
	"maps"
	"strings"
	"time"
)

// AlertmanagerPayload is the body Alertmanager posts to a generic webhook.
type AlertmanagerPayload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"` // firing | resolved
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

// Alert is one alert instance inside a webhook payload.
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	Fingerprint  string            `json:"fingerprint"`
	GeneratorURL string            `json:"generatorURL"`
}

// NormalizedAlert is the provider-neutral intake contract.
type NormalizedAlert struct {
	AlertIdentity string
	ServiceKey    string
	Environment   string
	Summary       string
	Resolved      bool
	EventTime     time.Time
	Symptoms      map[string]string
	ResourceHints map[string]string
}

// LabelMapping names which alert labels carry the service and environment.
type LabelMapping struct {
	ServiceLabel     string
	EnvironmentLabel string
}

// DefaultLabelMapping is the conventional mapping.
var DefaultLabelMapping = LabelMapping{ServiceLabel: "service", EnvironmentLabel: "environment"}

// Normalize converts one Alertmanager alert (with its payload context) into the
// intake contract. It returns ok=false when the alert lacks the service or
// environment label needed to route it, so the caller can record an intake
// failure instead of creating an unroutable incident.
func Normalize(p AlertmanagerPayload, a Alert, m LabelMapping) (NormalizedAlert, bool) {
	if m.ServiceLabel == "" {
		m = DefaultLabelMapping
	}
	labels := mergeLabels(p.CommonLabels, a.Labels)

	serviceKey := labels[m.ServiceLabel]
	if serviceKey == "" {
		serviceKey = labels["app"] // common fallback
	}
	environment := labels[m.EnvironmentLabel]
	if environment == "" {
		environment = labels["env"]
	}
	if serviceKey == "" || environment == "" {
		return NormalizedAlert{}, false
	}

	identity := a.Fingerprint
	if identity == "" {
		identity = labels["alertname"]
	}
	if identity == "" {
		identity = p.GroupKey
	}

	annotations := mergeLabels(p.CommonAnnotations, a.Annotations)
	summary := annotations["summary"]
	if summary == "" {
		summary = annotations["description"]
	}
	if summary == "" {
		summary = labels["alertname"]
	}

	eventTime := a.StartsAt
	resolved := strings.EqualFold(a.Status, "resolved") || strings.EqualFold(p.Status, "resolved")
	if resolved && !a.EndsAt.IsZero() {
		eventTime = a.EndsAt
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}

	return NormalizedAlert{
		AlertIdentity: identity,
		ServiceKey:    serviceKey,
		Environment:   environment,
		Summary:       summary,
		Resolved:      resolved,
		EventTime:     eventTime,
		Symptoms:      symptomsFrom(labels, annotations),
		ResourceHints: resourceHints(labels),
	}, true
}

func mergeLabels(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	maps.Copy(out, base)
	maps.Copy(out, overlay)
	return out
}

// symptomsFrom extracts human-relevant signal values from annotations and a few
// well-known labels, keeping the incident's symptom payload compact.
func symptomsFrom(labels, annotations map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"severity", "alertname"} {
		if v := labels[k]; v != "" {
			out[k] = v
		}
	}
	for k, v := range annotations {
		if k == "summary" || k == "description" {
			continue
		}
		out[k] = v
	}
	return out
}

// resourceHints pulls Kubernetes object hints from labels for later
// investigation targeting.
func resourceHints(labels map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"namespace", "pod", "deployment", "container", "cluster", "node"} {
		if v := labels[k]; v != "" {
			out[k] = v
		}
	}
	return out
}
