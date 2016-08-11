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

	"github.com/cenkalti/backoff"
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
		baseURL: u,

		maxInterval:    10 * time.Second,
		maxElapsedTime: time.Minute,
	}
}

func (as *APISelector) Select(params *Params) (string, error) {
	imageName, err := as.queryWithTags(params.Infra, as.buildCandidateTags(params))
	if err != nil {
		return "default", err
	}

	if imageName != "" {
		return imageName, nil
	}

	return "default", nil
}

func (as *APISelector) queryWithTags(infra string, tags []*tagSet) (string, error) {
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

	imageResp, err := as.makeImageRequest(u.String(), bodyLines)
	if err != nil {
		return "", err
	}

	if len(imageResp.Data) == 0 {
		return "", nil
	}

	return imageResp.Data[0].Name, nil
}

func (as *APISelector) makeImageRequest(urlString string, bodyLines []string) (*apiSelectorImageResponse, error) {
	var responseBody []byte

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	err := backoff.Retry(func() (err error) {
		resp, err := http.Post(urlString, imageAPIRequestContentType,
			strings.NewReader(strings.Join(bodyLines, "\n")+"\n"))

		if err != nil {
			return err
		}
		defer resp.Body.Close()
		responseBody, err = ioutil.ReadAll(resp.Body)
		return
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

func (as *APISelector) buildCandidateTags(params *Params) []*tagSet {
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

	if params.OS == "osx" && params.OsxImage != "" {
		addTags("osx_image:"+params.OsxImage, "os:osx")
	}

	if params.Dist != "" && params.Group != "" && hasLang {
		addTags("dist:"+params.Dist, "group:"+params.Group, "language_"+params.Language+":true")
		addTags("dist:"+params.Dist, "group_"+params.Group+":true", "language_"+params.Language+":true")
	}

	if params.Dist != "" && hasLang {
		addTags("dist:"+params.Dist, "language_"+params.Language+":true")
	}

	if params.Group != "" && hasLang {
		addTags("group:"+params.Group, "language_"+params.Language+":true")
		addTags("group_"+params.Group+":true", "language_"+params.Language+":true")
	}

	if params.OS != "" && hasLang {
		addTags("os:"+params.OS, "language_"+params.Language+":true")
	}

	if hasLang {
		addDefaultTag("language_" + params.Language + ":true")
	}

	if params.OS == "osx" && params.OsxImage != "" {
		addDefaultTag("osx_image:" + params.OsxImage)
	}

	if params.Dist != "" {
		addDefaultTag("dist:" + params.Dist)
	}

	if params.Group != "" {
		addDefaultTag("group:" + params.Group)
		addDefaultTag("group_" + params.Group + ":true")
	}

	if params.OS != "" {
		addDefaultTag("os:" + params.OS)
	}

	result := append([]*tagSet{fullTagSet}, candidateTags...)
	for _, ts := range result {
		sort.Strings(ts.Tags)
	}

	return result
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
