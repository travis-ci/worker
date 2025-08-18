package image

import (
	"bytes"
	gocontext "context"
	"encoding/base64"
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
	Os            string `json:"os"`
	OsVersion     string `json:"os_version"`
	Labels        string `json:"labels"`
	Description   string `json:"description"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	ToBeDeletedAt string `json:"to_be_deleted_at"`
}

type artifactManagerImageResponse struct {
	Image *ArtifactManagerImage `json:"image"`
}

type ArtifactManager struct {
	baseURL   string
	authToken string
}

func NewArtifactManager(u string, t string) *ArtifactManager {
	return &ArtifactManager{
		baseURL:   u,
		authToken: t,
	}
}

func (am *ArtifactManager) UpdateFailedImage(ctx gocontext.Context, customImageId int, reason string, userId int) (bool, error) {
	d := map[string]string{
		"state":  "error",
		"reason": reason,
	}
	return am.Patch(ctx, customImageId, d, userId)
}

func (am *ArtifactManager) UpdateCreatingImage(ctx gocontext.Context, customImageId int, userId int) (bool, error) {
	d := map[string]string{
		"state": "creating",
	}
	return am.Patch(ctx, customImageId, d, userId)
}

func (am *ArtifactManager) UpdateImage(ctx gocontext.Context, customImageId int, size int64, architecture string,os string, os_version string, userId int) (bool, error) {
	d := map[string]string{
		"size_bytes":   strconv.FormatInt(size, 10),
		"architecture": architecture,
		"os":           os,
		"os_version":   os_version,
	}
	return am.Patch(ctx, customImageId, d, userId)
}

func (am *ArtifactManager) UseImage(ctx gocontext.Context, customImageId int, userId int) (ArtifactManagerImage, error) {
	client := &http.Client{}
	url := fmt.Sprintf("%s/image/%d/use", am.baseURL, customImageId)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader([]byte("")))
	if err != nil {
		return ArtifactManagerImage{}, fmt.Errorf("failed to make http request: %s", err)
	}

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}

	req.Header.Add("Content-Type", "application/json")
	sEnc := base64.StdEncoding.EncodeToString([]byte("_:" + am.authToken))
	req.Header.Add("Authorization", "Basic "+sEnc)
	req.Header.Add("X-Travis-User-Id", strconv.Itoa(userId))
	req.Header.Add("From", processorID)
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return ArtifactManagerImage{}, fmt.Errorf("failed to call http request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return ArtifactManagerImage{}, fmt.Errorf("image not available")
	}

	if resp.StatusCode != 200 {
		return ArtifactManagerImage{}, fmt.Errorf("ArtifactManager response code: %d, response body: %s", resp.StatusCode, resp.Body)
	}

	var imageResp artifactManagerImageResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ArtifactManagerImage{}, err
	}
	err = json.Unmarshal(responseBody, &imageResp)
	if err != nil || imageResp.Image == nil {
		return ArtifactManagerImage{}, fmt.Errorf("error at ArtifactManager response: %s", string(responseBody))
	}
	return *imageResp.Image, nil
}

func (am *ArtifactManager) GenerateCustomImageName(ownerId int, ownerType string, customImageId int) string {
	return fmt.Sprintf("customimage-%d-%s-%d", ownerId, ownerType, customImageId)
}

func (am *ArtifactManager) GetImage(ctx gocontext.Context, usedCustomImageName string, userId int, ownerId int, ownerType string) (ArtifactManagerImage, error) {
	client := &http.Client{}
	url := fmt.Sprintf("%s/image/%s/%d/%s", am.baseURL, ownerType, ownerId, usedCustomImageName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ArtifactManagerImage{}, fmt.Errorf("failed to make http request GET %s  %s", url, err)
	}

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}

	req.Header.Add("Content-Type", "application/json")
	sEnc := base64.StdEncoding.EncodeToString([]byte("_:" + am.authToken))
	req.Header.Add("Authorization", "Basic "+sEnc)
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
		return ArtifactManagerImage{}, errors.Errorf("expected 200 status code from ArtifactManager: %s, received status=%d body=%q",
			url,
			resp.StatusCode,
			responseBody)
	}

	var imageResp artifactManagerImageResponse

	err = json.Unmarshal(responseBody, &imageResp)
	if err != nil {
		return ArtifactManagerImage{}, err
	}
	return *imageResp.Image, nil
}

func (am *ArtifactManager) Patch(ctx gocontext.Context, customImageId int, data map[string]string, userId int) (bool, error) {
	client := &http.Client{}

	marshalled, err := json.Marshal(data)
	if err != nil {
		return false, fmt.Errorf("failed to marshall in UpdateImageSize: %s", err)
	}
	url := fmt.Sprintf("%s/image/%d", am.baseURL, customImageId)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(marshalled))
	if err != nil {
		return false, fmt.Errorf("failed to make http request: %s", err)
	}

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}

	req.Header.Add("Content-Type", "application/json")
	sEnc := base64.StdEncoding.EncodeToString([]byte("_:" + am.authToken))
	req.Header.Add("Authorization", "Basic "+sEnc)
	req.Header.Add("X-Travis-User-Id", strconv.Itoa(userId))
	req.Header.Add("From", processorID)
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to call http request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false, fmt.Errorf("ArtifactManager response code: %d, response body: %s", resp.StatusCode, resp.Body)
	}

	return resp.StatusCode == 200, nil
}
