package worker

import (
	"bytes"
	gocontext "context"
	"encoding/json"
	"fmt"
	"net/http"
)

var artifactManagerAPIURI string

func SetupArtifactManager(uri string) {
	artifactManagerAPIURI = uri
}

func UpdateArtifactSize(ctx gocontext.Context, customImageId int, size int64) (bool, error) {
	client := &http.Client{}
	d := map[string]int64{
		"size": size,
	}
	marshalled, err := json.Marshal(d)
	if err != nil {
		return false, fmt.Errorf("failed to marshall in updateArtifactSize: %s", err)
	}
	url := fmt.Sprintf("%s/image/%d", artifactManagerAPIURI, customImageId)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(marshalled))
	if err != nil {
		return false, fmt.Errorf("failed to make http request: %s", err)
	}

	req.Header.Add("Content-Type", "application/json")
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to call http request: %s", err)
	}

	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

func GenerateCustomImageName(ownerId int, ownerType string, customImageId int) string {
	return fmt.Sprintf("%d_%s_%d", ownerId, ownerType, customImageId)
}
