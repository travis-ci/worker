/*
	Package librato is a library for Librato's metrics service API.

	Example:

		metrics = &librato.Metrics{"login@email.com", "token"}
		metrics := &librato.MetricsFormat{
			Counters: []librato.Metrics{librato.Metric{"name", 123, "source"}},
			Gauges: []librato.Metrics{},
		}
		metrics.SendMetrics(metrics)
*/
package librato

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	metricsApiUrl         = "https://metrics-api.librato.com/v1/metrics"
	metricsUsersApiUrl    = "https://api.librato.com/v1/users"
	metricsServicesApiUrl = "https://metrics-api.librato.com/v1/services"
)

type Metric struct {
	Name        string  `json:"name"`
	Value       float64 `json:"value"`
	MeasureTime int64   `json:"measure_time,omitempty"`
	Source      string  `json:"source,omitempty"`
}

type Gauge struct {
	Name        string  `json:"name"`
	MeasureTime int64   `json:"measure_time,omitempty"`
	Source      string  `json:"source,omitempty"`
	Count       uint64  `json:"count"`
	Sum         float64 `json:"sum"`
	Max         float64 `json:"max,omitempty"`
	Min         float64 `json:"min,omitempty"`
	SumSquares  float64 `json:"sum_squares,omitempty"`
}

type MetricsFormat struct {
	MeasureTime int64         `json:"measure_time,omitempty"`
	Source      string        `json:"source,omitempty"`
	Counters    []Metric      `json:"counters,omitempty"`
	Gauges      []interface{} `json:"gauges,omitempty"`
}

type Metrics struct {
	Username string
	Token    string
}

type ErrTypes struct {
	Params  map[string]interface{} `json:"params"`
	Request []string               `json:"request"`
	System  []string               `json:"system"`
}

type ErrResponse struct {
	Errors ErrTypes `json:"errors"`
}

func (e *ErrResponse) Error() string {
	return fmt.Sprintf("{Err %+v}", e.Errors)
}

// Crete and submit measurements for new or existing metrics.
// http://dev.librato.com/v1/post/metrics
func (met *Metrics) SendMetrics(metrics *MetricsFormat) error {
	if len(metrics.Counters) == 0 && len(metrics.Gauges) == 0 {
		return nil
	}

	js, err := json.Marshal(metrics)
	if err != nil {
		return err
	}

	return met.post(metricsApiUrl, bytes.NewBuffer(js))
}

func (met *Metrics) request(method string, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", userAgent)
	req.SetBasicAuth(met.Username, met.Token)
	res, err := http.DefaultClient.Do(req)
	return res, err
}

func (met *Metrics) get(url string) (*http.Response, error) {
	return met.request("GET", url, nil)
}

func (met *Metrics) post(url string, body io.Reader) error {
	res, err := met.request("POST", url, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		b := make([]byte, 1024)
		if n, err := res.Body.Read(b); err == nil {
			errRes := &ErrResponse{}
			if err := json.Unmarshal(b[:n], errRes); err == nil {
				return errRes
			}
		}
		return errors.New(res.Status)
	}

	return nil
}
