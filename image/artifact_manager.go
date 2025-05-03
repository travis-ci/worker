package image

import (
	"bytes"
	gocontext "context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/pkg/errors"
	"github.com/travis-ci/worker/context"
)

type ArtifactManagerImage struct {
	Id            int    `json:"id"`
	OwnertType    string `json:"owner_type"`
	OwnerId       int    `json:"owner_id"`
	Name          string `json:"name"`
	Usage         int    `json:"usage"`
	Architecture  string `json:"architecture"`
	GoogleId      string `json:"google_id"`
	State         string `json:"state"`
	SizeBytes     int64  `json:"size_bytes"`
	OsVersion     string `json:"os_version"`
	Labels        string `json:"labels"`
	Description   string `json:"description"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	ToBeDeletedAt string `json:"to_be_deleted_at"`
}

type artifaxctManagerImageResponse struct {
	Data *ArtifactManagerImage `json:"data"`
}

type ArtifactManager struct {
	baseURL string
}

func NewArtifactManager(u string) *ArtifactManager {
	return &ArtifactManager{
		baseURL: u,
	}
}

func (am *ArtifactManager) UpdateImageSize(ctx gocontext.Context, customImageId int, size int64) (bool, error) {
	client := &http.Client{}
	d := map[string]int64{
		"size_bytes": size,
	}
	marshalled, err := json.Marshal(d)
	if err != nil {
		return false, fmt.Errorf("failed to marshall in UpdateImageSize: %s", err)
	}
	url := fmt.Sprintf("%s/image/%d", am.baseURL, customImageId)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(marshalled))
	if err != nil {
		return false, fmt.Errorf("failed to make http request: %s", err)
	}

	jwt, ok := context.JWTFromContext(ctx)
	if !ok {
		return false, fmt.Errorf("failed to UpdateImageSize; no jwt in context")
	}

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "Bearer "+jwt)
	req.Header.Add("From", processorID)
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to call http request: %s", err)
	}

	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

func (am *ArtifactManager) GenerateCustomImageName(ownerId int, ownerType string, customImageId int) string {
	return fmt.Sprintf("%d_%s_%d", ownerId, ownerType, customImageId)
}

func (am *ArtifactManager) GetImage(ctx gocontext.Context, customImageId int, userId int) (ArtifactManagerImage, error) {
	client := &http.Client{}
	url := fmt.Sprintf("%s/image/%d/use", am.baseURL, customImageId)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ArtifactManagerImage{}, fmt.Errorf("failed to make http request: %s", err)
	}

	jwt, ok := context.JWTFromContext(ctx)
	if !ok {
		return ArtifactManagerImage{}, fmt.Errorf("failed to GetImage; no jwt in context")
	}

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "Bearer "+jwt)
	req.Header.Add("From", processorID)
	req.Header.Add("HTTP_X_TRAVIS_USER_ID", strconv.Itoa(userId))
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return ArtifactManagerImage{}, fmt.Errorf("failed to call http request: %s", err)
	}

	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ArtifactManagerImage{}, err
	}

	if resp.StatusCode != 200 {
		return ArtifactManagerImage{}, errors.Errorf("expected 200 status code from job-board, received status=%d body=%q",
			resp.StatusCode,
			responseBody)
	}

	var imageResp artifaxctManagerImageResponse

	err = json.Unmarshal(responseBody, &imageResp)
	if err != nil {
		return ArtifactManagerImage{}, err
	}
	return *imageResp.Data, nil
}
