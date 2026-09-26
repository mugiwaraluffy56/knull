package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sandboxNamespace = regexp.MustCompile(`^knull-run-[a-f0-9]{20}$`)

// PrometheusObserver queries a sandbox-local Prometheus endpoint. Both query
// templates must contain {{namespace}}, binding samples to this run only.
// Configure error-rate and p95 latency expressions from the chosen sandbox
// load driver's metrics. A missing series is an error, never a healthy zero.
type PrometheusObserver struct {
	BaseURL              string
	Token                string
	ErrorRateQuery       string
	P95MillisecondsQuery string
	Client               *http.Client
}

func (o PrometheusObserver) ObserveMetrics(ctx context.Context, namespace string) (MetricObservation, error) {
	if !sandboxNamespace.MatchString(namespace) || strings.Count(o.ErrorRateQuery, "{{namespace}}") != 1 || strings.Count(o.P95MillisecondsQuery, "{{namespace}}") != 1 {
		return MetricObservation{}, fmt.Errorf("%w: namespace-scoped Prometheus queries required", ErrFailed)
	}
	base, err := url.Parse(o.BaseURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return MetricObservation{}, fmt.Errorf("%w: invalid sandbox Prometheus URL", ErrFailed)
	}
	end := time.Now().UTC()
	start := end.Add(-90 * time.Second)
	errorValues, err := o.queryRange(ctx, base, strings.Replace(o.ErrorRateQuery, "{{namespace}}", namespace, 1), start, end)
	if err != nil {
		return MetricObservation{}, err
	}
	latencyValues, err := o.queryRange(ctx, base, strings.Replace(o.P95MillisecondsQuery, "{{namespace}}", namespace, 1), start, end)
	if err != nil {
		return MetricObservation{}, err
	}
	if len(errorValues) < 10 || len(latencyValues) < 10 {
		return MetricObservation{}, fmt.Errorf("%w: too few sandbox metric samples", ErrFailed)
	}
	result := MetricObservation{Samples: min(len(errorValues), len(latencyValues)), WindowStart: start, WindowEnd: end}
	for _, v := range errorValues {
		if v < 0 || v > 1 {
			return MetricObservation{}, fmt.Errorf("%w: invalid error rate", ErrFailed)
		}
		result.ErrorRate = math.Max(result.ErrorRate, v)
	}
	for _, v := range latencyValues {
		if v < 0 {
			return MetricObservation{}, fmt.Errorf("%w: invalid latency", ErrFailed)
		}
		result.P95Milliseconds = math.Max(result.P95Milliseconds, v)
	}
	return result, nil
}

func (o PrometheusObserver) queryRange(ctx context.Context, base *url.URL, expression string, start, end time.Time) ([]float64, error) {
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query_range"
	query := u.Query()
	query.Set("query", expression)
	query.Set("start", strconv.FormatFloat(float64(start.Unix()), 'f', 0, 64))
	query.Set("end", strconv.FormatFloat(float64(end.Unix()), 'f', 0, 64))
	query.Set("step", "10s")
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if o.Token != "" {
		request.Header.Set("Authorization", "Bearer "+o.Token)
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: Prometheus query: %v", ErrFailed, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: Prometheus status %d", ErrFailed, response.StatusCode)
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
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode Prometheus response: %v", ErrFailed, err)
	}
	if payload.Status != "success" || payload.Data.ResultType != "matrix" || len(payload.Data.Result) != 1 {
		return nil, fmt.Errorf("%w: missing or ambiguous Prometheus series", ErrFailed)
	}
	values := make([]float64, 0, len(payload.Data.Result[0].Values))
	for _, sample := range payload.Data.Result[0].Values {
		if len(sample) != 2 {
			return nil, fmt.Errorf("%w: invalid Prometheus sample", ErrFailed)
		}
		var timestamp float64
		var raw string
		if err := json.Unmarshal(sample[0], &timestamp); err != nil {
			return nil, fmt.Errorf("%w: sample time: %v", ErrFailed, err)
		}
		if err := json.Unmarshal(sample[1], &raw); err != nil {
			return nil, fmt.Errorf("%w: sample value: %v", ErrFailed, err)
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || timestamp < float64(start.Unix())-1 || timestamp > float64(end.Unix())+1 {
			return nil, fmt.Errorf("%w: out-of-window or invalid Prometheus sample", ErrFailed)
		}
		values = append(values, value)
	}
	return values, nil
}

var _ MetricObserver = PrometheusObserver{}
