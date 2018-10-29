package image

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	gocontext "context"

	"github.com/cenk/backoff"
	"github.com/pkg/errors"
	workererrors "github.com/travis-ci/worker/errors"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
)

const (
	imageAPIRequestContentType = "application/x-www-form-urlencoded; boundary=NL"
)

type APISelector struct {
	baseURL *url.URL

	maxInterval    time.Duration
	maxElapsedTime time.Duration
}

func NewAPISelector(u *url.URL) *APISelector {
	return &APISelector{
		baseURL:        u,
		maxInterval:    10 * time.Second,
		maxElapsedTime: time.Minute,
	}
}

func (as *APISelector) SetMaxInterval(maxInterval time.Duration) {
	as.maxInterval = maxInterval
}

func (as *APISelector) SetMaxElapsedTime(maxElapsedTime time.Duration) {
	as.maxElapsedTime = maxElapsedTime
}

func (as *APISelector) Select(ctx gocontext.Context, params *Params) (string, error) {
	tagSets, err := as.buildCandidateTags(params)
	if err != nil {
		return "default", err
	}

	imageName, err := as.queryWithTags(ctx, params.Infra, tagSets)
	if err != nil {
		return "default", err
	}

	if imageName != "" {
		return imageName, nil
	}

	return "default", nil
}

func (as *APISelector) queryWithTags(ctx gocontext.Context, infra string, tags []*tagSet) (string, error) {
	ctx, span := trace.StartSpan(ctx, "APISelector.querywithTags")
	defer span.End()

	bodyLines := []string{}
	lastJobID := uint64(0)
	lastRepo := ""

	for _, ts := range tags {
		qs := url.Values{}
		qs.Set("infra", infra)
		qs.Set("fields[images]", "name")
		qs.Set("limit", "1")
		qs.Set("job_id", fmt.Sprintf("%v", ts.JobID))
		qs.Set("repo", ts.Repo)
		qs.Set("is_default", fmt.Sprintf("%v", ts.IsDefault))
		if len(ts.Tags) > 0 {
			qs.Set("tags", strings.Join(ts.Tags, ","))
		}

		bodyLines = append(bodyLines, qs.Encode())
		lastJobID = ts.JobID
		lastRepo = ts.Repo
	}

	qs := url.Values{}
	qs.Set("infra", infra)
	qs.Set("is_default", "true")
	qs.Set("fields[images]", "name")
	qs.Set("limit", "1")
	qs.Set("job_id", fmt.Sprintf("%v", lastJobID))
	qs.Set("repo", lastRepo)

	bodyLines = append(bodyLines, qs.Encode())

	u, err := url.Parse(as.baseURL.String())
	if err != nil {
		return "", err
	}

	imageResp, err := as.makeImageRequest(ctx, u.String(), bodyLines)
	if err != nil {
		return "", err
	}

	if len(imageResp.Data) == 0 {
		return "", nil
	}

	return imageResp.Data[0].Name, nil
}

func (as *APISelector) makeImageRequest(ctx gocontext.Context, urlString string, bodyLines []string) (*apiSelectorImageResponse, error) {
	var responseBody []byte

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = as.maxInterval
	b.MaxElapsedTime = as.maxElapsedTime

	client := &http.Client{
		Transport: &ochttp.Transport{},
	}

	err := backoff.Retry(func() error {
		req, err := http.NewRequest("POST", urlString, strings.NewReader(strings.Join(bodyLines, "\n")+"\n"))
		if err != nil {
			return err
		}

		req.Header.Add("Content-Type", imageAPIRequestContentType)
		req = req.WithContext(ctx)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		responseBody, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		if resp.StatusCode != 200 {
			return errors.Errorf("expected 200 status code from job-board, received status=%d body=%q",
				resp.StatusCode,
				responseBody)
		}

		return nil
	}, b)

	if err != nil {
		return nil, err
	}

	imageResp := &apiSelectorImageResponse{
		Data: []*apiSelectorImageRef{},
	}

	err = json.Unmarshal(responseBody, imageResp)
	if err != nil {
		return nil, err
	}

	return imageResp, nil
}

type tagSet struct {
	Tags      []string
	IsDefault bool

	JobID uint64
	Repo  string
}

func (ts *tagSet) GoString() string {
	return fmt.Sprintf("&image.tagSet{IsDefault: %v, Tags: %#v}", ts.IsDefault, ts.Tags)
}

func (as *APISelector) buildCandidateTags(params *Params) ([]*tagSet, error) {
	fullTagSet := &tagSet{
		Tags:  []string{},
		JobID: params.JobID,
		Repo:  params.Repo,
	}
	candidateTags := []*tagSet{}

	addDefaultTag := func(tag string) {
		fullTagSet.Tags = append(fullTagSet.Tags, tag)
		candidateTags = append(candidateTags,
			&tagSet{
				IsDefault: true,
				Tags:      []string{tag},
				JobID:     params.JobID,
				Repo:      params.Repo,
			})
	}

	addTags := func(tags ...string) {
		candidateTags = append(candidateTags,
			&tagSet{
				IsDefault: false,
				Tags:      tags,
				JobID:     params.JobID,
				Repo:      params.Repo,
			})
	}

	hasLang := params.Language != ""
	hasDist := params.Dist != ""
	hasGroup := params.Group != ""
	hasOS := params.OS != ""

	if params.OS == "osx" && params.OsxImage != "" {
		addTags("osx_image:"+params.OsxImage, "os:osx")
	}

	if hasDist && hasGroup && hasLang {
		addTags("dist:"+params.Dist, "group_"+params.Group+":true", "language_"+params.Language+":true")
	}

	if hasDist && hasLang {
		addTags("dist:"+params.Dist, "language_"+params.Language+":true")
	}

	if hasGroup && hasLang {
		addTags("group_"+params.Group+":true", "language_"+params.Language+":true")
	}

	if hasOS && hasLang {
		addTags("os:"+params.OS, "language_"+params.Language+":true")
	}

	if hasLang {
		addDefaultTag("language_" + params.Language + ":true")
	}

	if params.OS == "osx" && params.OsxImage != "" {
		addDefaultTag("osx_image:" + params.OsxImage)
	}

	if hasDist {
		addDefaultTag("dist:" + params.Dist)
	}

	if hasGroup {
		addDefaultTag("group_" + params.Group + ":true")
	}

	if hasOS {
		addDefaultTag("os:" + params.OS)
	}

	result := append([]*tagSet{fullTagSet}, candidateTags...)
	for _, ts := range result {
		sort.Strings(ts.Tags)
	}

	for _, ts := range result {
		for _, tag := range ts.Tags {
			if strings.Contains(tag, ",") {
				return result, workererrors.NewWrappedJobAbortError(errors.Errorf("job was aborted because tag \"%v\" contained \",\", this can happen when .travis.yml has a trailing comma", tag))
			}
		}
	}

	return result, nil
}

type apiSelectorImageResponse struct {
	Data []*apiSelectorImageRef `json:"data"`
}

type apiSelectorImageRef struct {
	ID        int               `json:"id"`
	Infra     string            `json:"infra"`
	Name      string            `json:"name"`
	Tags      map[string]string `json:"tags"`
	IsDefault bool              `json:"is_default"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}
