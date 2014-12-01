package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

type BuildScriptGenerator struct {
	endpoint string
	apiKey   string
}

func NewBuildScriptGenerator(endpoint string, apiKey string) *BuildScriptGenerator {
	return &BuildScriptGenerator{endpoint, apiKey}
}

func (bsg *BuildScriptGenerator) GenerateForPayload(payload Payload) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest("POST", bsg.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "travis-worker")
	request.Header.Set("Authorization", fmt.Sprintf("token %s", bsg.apiKey))

	client := http.Client{}

	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(resp.Body)
}
