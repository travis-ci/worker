package librato

import (
	"encoding/json"
	"errors"
)

type Service struct {
	ID       int               `json:"id"`
	Type     string            `json:"type"`
	Settings map[string]string `json:"settings"`
	Title    string            `json:"title"`
}

type ServicesResponse struct {
	Query    QueryResponse `json:"query"`
	Services []Service     `json:"service"`
}

// Return all services created by the user.
// http://dev.librato.com/v1/get/services
func (met *Metrics) GetServices() (*ServicesResponse, error) {
	res, err := met.get(metricsServicesApiUrl)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != 200 {
		return nil, errors.New(res.Status)
	}

	var svc ServicesResponse
	jdec := json.NewDecoder(res.Body)
	err = jdec.Decode(&svc)
	return &svc, err
}
