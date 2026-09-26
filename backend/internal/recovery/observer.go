package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/recoverypolicy"
)

var ErrSignal = errors.New("production recovery signal unavailable")

// LiveObserver reads one configured workload and namespace-scoped Prometheus
// series. It has no mutation methods or fallback to missing-as-zero.
type LiveObserver struct {
	Kubernetes                      kubernetes.Interface
	Cluster, Namespace, Workload    string
	PrometheusURL, PrometheusToken  string
	ErrorRateQuery, LatencyP95Query string
	HTTP                            *http.Client
}

func (o LiveObserver) Observe(ctx context.Context, target actions.Target, policy recoverypolicy.Policy, start, end time.Time) (recoverypolicy.Observation, error) {
	if o.Kubernetes == nil || target.Cluster != o.Cluster || target.Namespace != o.Namespace || target.Name != o.Workload || target.Kind != "Deployment" || policy.Validate() != nil || !end.After(start) {
		return recoverypolicy.Observation{}, ErrSignal
	}
	deployment, err := o.Kubernetes.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		return recoverypolicy.Observation{}, fmt.Errorf("%w: deployment read: %v", ErrSignal, err)
	}
	if deployment.Spec.Replicas == nil {
		return recoverypolicy.Observation{}, ErrSignal
	}
	desired := int(*deployment.Spec.Replicas)
	ready := int(deployment.Status.ReadyReplicas)
	obs := recoverypolicy.Observation{WindowStart: start, WindowEnd: end, DesiredReplicas: &desired, ReadyReplicas: &ready}
	if policy.ErrorRate != nil {
		value, samples, err := o.queryRange(ctx, o.ErrorRateQuery, target.Namespace, start, end)
		if err == nil {
			obs.ErrorRateRatio, obs.ErrorRateSamples = &value, &samples
		}
	}
	if policy.LatencyP95 != nil {
		value, samples, err := o.queryRange(ctx, o.LatencyP95Query, target.Namespace, start, end)
		if err == nil {
			obs.LatencyP95Milliseconds, obs.LatencySamples = &value, &samples
		}
	}
	return obs, nil
}

func (o LiveObserver) queryRange(ctx context.Context, template, namespace string, start, end time.Time) (float64, int, error) {
	if strings.Count(template, "{{namespace}}") != 1 || namespace == "" {
		return 0, 0, ErrSignal
	}
	base, err := url.Parse(o.PrometheusURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return 0, 0, ErrSignal
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/v1/query_range"
	query := base.Query()
	query.Set("query", strings.Replace(template, "{{namespace}}", namespace, 1))
	query.Set("start", strconv.FormatInt(start.Unix(), 10))
	query.Set("end", strconv.FormatInt(end.Unix(), 10))
	query.Set("step", "10s")
	base.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return 0, 0, err
	}
	if o.PrometheusToken != "" {
		request.Header.Set("Authorization", "Bearer "+o.PrometheusToken)
	}
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: Prometheus request: %v", ErrSignal, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, 0, ErrSignal
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload) != nil || payload.Status != "success" || payload.Data.ResultType != "matrix" || len(payload.Data.Result) != 1 {
		return 0, 0, ErrSignal
	}
	maximum, samples := 0.0, 0
	for _, pair := range payload.Data.Result[0].Values {
		if len(pair) != 2 {
			return 0, 0, ErrSignal
		}
		var timestamp float64
		var raw string
		if json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &raw) != nil {
			return 0, 0, ErrSignal
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || timestamp < float64(start.Unix())-1 || timestamp > float64(end.Unix())+1 {
			return 0, 0, ErrSignal
		}
		maximum = math.Max(maximum, value)
		samples++
	}
	if samples == 0 {
		return 0, 0, ErrSignal
	}
	return maximum, samples, nil
}

var _ Observer = LiveObserver{}
