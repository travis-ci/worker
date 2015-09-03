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
}

func NewAPISelector(u *url.URL) *APISelector {
	return &APISelector{baseURL: u}
}

func (as *APISelector) Select(params *Params) (string, error) {
	imageName := "default"

	fullTagSet := []string{}

	if params.Language != "" {
		fullTagSet = append(fullTagSet, "language:"+params.Language)
	}

	if params.Dist != "" {
		fullTagSet = append(fullTagSet, "dist:"+params.Dist)
	}

	if params.Group != "" {
		fullTagSet = append(fullTagSet, "group:"+params.Group)
	}

	if params.OS != "" {
		fullTagSet = append(fullTagSet, "os:"+params.OS)
	}

	tagAttempts := [][]string{}

	if params.OS == "osx" && params.OsxImage != "" {
		fullTagSet = append(fullTagSet, "osx_image:"+params.OsxImage)
		tagAttempts = append(tagAttempts,
			[]string{"osx_image:" + params.OsxImage, "language:" + params.Language},
			[]string{"osx_image:" + params.OsxImage})
	}

	tagAttempts = append(tagAttempts,
		fullTagSet,
		[]string{"language:" + params.Language},
		[]string{"language_" + params.Language + ":true"},
		[]string{"dist:" + params.Dist, "language:" + params.Language},
		[]string{"dist:" + params.Dist, "language_" + params.Language + ":true"},
		[]string{"dist:" + params.Dist},
		[]string{"group:" + params.Group, "language:" + params.Language},
		[]string{"group:" + params.Group, "language_" + params.Language + ":true"},
		[]string{"group:" + params.Group},
		[]string{"os:" + params.OsxImage, "language:" + params.Language},
		[]string{"os:" + params.OsxImage, "language_" + params.Language + ":true"},
		[]string{"os:" + params.OsxImage})

	for _, tags := range tagAttempts {
		u, _ := url.Parse(as.baseURL.String())
		qs := url.Values{}
		qs.Set("infra", params.Infra)
		qs.Set("limit", "1")
		qs.Set("tags", strings.Join(tags, ","))

		u.RawQuery = qs.Encode()

		var body []byte

		b := backoff.NewExponentialBackOff()
		b.MaxInterval = 10 * time.Second
		b.MaxElapsedTime = time.Minute

		err := backoff.Retry(func() (err error) {
			resp, err := http.Get(u.String())
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, err = ioutil.ReadAll(resp.Body)
			return
		}, b)

		if err != nil {
			return "", err
		}

		imageResp := &apiSelectorImageResponse{
			Data: []*apiSelectorImageRef{},
		}

		err = json.Unmarshal(body, imageResp)
		if err != nil {
			return "", err
		}

		if len(imageResp.Data) > 0 {
			return imageResp.Data[0].Name, nil
		}
	}
	return imageName, nil
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
