package image

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
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
	for _, tags := range as.buildCandidateTags(params) {
		imageName, err := as.queryWithTags(params.Infra, tags)
		if err != nil {
			return "default", err
		}

		if imageName != "" {
			return imageName, nil
		}
	}

	return "default", nil
}

func (as *APISelector) queryWithTags(infra string, tags []string) (string, error) {
	u, _ := url.Parse(as.baseURL.String())
	qs := url.Values{}
	qs.Set("infra", infra)
	qs.Set("limit", "1")
	if len(tags) > 0 {
		qs.Set("tags", strings.Join(tags, ","))
	}

	u.RawQuery = qs.Encode()

	imageResp, err := as.makeImageRequest(u.String())
	if err != nil {
		return "", err
	}

	if len(imageResp.Data) > 0 {
		return imageResp.Data[0].Name, nil
	}

	return "", nil
}

func (as *APISelector) makeImageRequest(urlString string) (*apiSelectorImageResponse, error) {
	var body []byte

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	err := backoff.Retry(func() (err error) {
		resp, err := http.Get(urlString)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err = ioutil.ReadAll(resp.Body)
		return
	}, b)

	if err != nil {
		return nil, err
	}

	imageResp := &apiSelectorImageResponse{
		Data: []*apiSelectorImageRef{},
	}

	err = json.Unmarshal(body, imageResp)
	if err != nil {
		return nil, err
	}

	return imageResp, nil
}

func (as *APISelector) buildCandidateTags(params *Params) [][]string {
	fullTagSet := []string{}
	candidateTags := [][]string{}

	addTag := func(tag string) {
		fullTagSet = append(fullTagSet, tag)
		candidateTags = append(candidateTags, []string{tag})
	}

	addTags := func(tags ...string) {
		candidateTags = append(candidateTags, tags)
	}

	hasLang := params.Language != ""

	if params.OS == "osx" && params.OsxImage != "" && hasLang {
		addTags("osx_image:"+params.OsxImage, "language:"+params.Language)
		addTags("osx_image:"+params.OsxImage, "language_"+params.Language+":true")
	}

	if params.Dist != "" && hasLang {
		addTags("dist:"+params.Dist, "language:"+params.Language)
		addTags("dist:"+params.Dist, "language_"+params.Language+":true")
	}

	if params.Group != "" && hasLang {
		addTags("group:"+params.Group, "language:"+params.Language)
		addTags("group:"+params.Group, "language_"+params.Language+":true")
	}

	if params.OS != "" && hasLang {
		addTags("os:"+params.OS, "language:"+params.Language)
		addTags("os:"+params.OS, "language_"+params.Language+":true")
	}

	if hasLang {
		addTag("language:" + params.Language)
		addTag("language_" + params.Language + ":true")
	}

	if params.OS == "osx" && params.OsxImage != "" {
		addTag("osx_image:" + params.OsxImage)
	}

	if params.Dist != "" {
		addTag("dist:" + params.Dist)
	}

	if params.Group != "" {
		addTag("group:" + params.Group)
	}

	if params.OS != "" {
		addTag("os:" + params.OS)
	}

	return append([][]string{fullTagSet}, candidateTags...)
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
