package image

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

type APISelector struct {
	baseURL *url.URL
}

func NewAPISelector(u *url.URL) *APISelector {
	return &APISelector{baseURL: u}
}

func (as *APISelector) Select(params *Params) (string, error) {
	imageName := "default"

	u, _ := url.Parse(as.baseURL.String())

	qs := url.Values{}
	qs.Set("infra", params.Infra)
	qs.Set("limit", "1")

	tags := []string{}

	if params.Language != "" {
		tags = append(tags, fmt.Sprintf("language:%s", params.Language))
	}

	if params.OS != "" {
		tags = append(tags, fmt.Sprintf("os:%s", params.OS))
	}

	if params.OS == "osx" && params.OsxImage != "" {
		tags = append(tags, fmt.Sprintf("osx_image:%s", params.OsxImage))
	}

	if params.Dist != "" {
		tags = append(tags, fmt.Sprintf("dist:%s", params.Dist))
	}

	if params.Group != "" {
		tags = append(tags, fmt.Sprintf("group:%s", params.Group))
	}

	qs.Set("tags", strings.Join(tags, ","))

	u.RawQuery = qs.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
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

	if len(imageResp.Data) == 0 {
		return imageName, nil
	}

	return imageResp.Data[0].Name, nil
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
