package backend

import (
	"fmt"
	//"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	//"net/url"
	//"os"
	//"path/filepath"
	"encoding/json"
	"testing"

	//"github.com/stretchr/testify/assert"
	"github.com/codegangsta/cli"
	"github.com/fsouza/go-dockerclient"
	"golang.org/x/net/context"
)

var (
	dockerTestMux      *http.ServeMux
	dockerTestProvider *dockerProvider
	dockerTestServer   *httptest.Server
)

func dockerTestSetup(t *testing.T, c *cli.Context) {
	t.SkipNow()
	dockerTestMux = http.NewServeMux()
	dockerTestServer = httptest.NewServer(dockerTestMux)
	// c.Set("ENDPOINT", dockerTestServer.URL)
	provider, _ := newDockerProvider(c)
	dockerTestProvider = provider.(*dockerProvider)
}

func dockerTestTeardown() {
	dockerTestServer.Close()
	dockerTestMux = nil
	dockerTestServer = nil
	dockerTestProvider = nil
}

type containerCreateRequest struct {
	Image      string            `json:"Image"`
	HostConfig docker.HostConfig `json:"HostConfig"`
}

func TestDockerStart(t *testing.T) {
	t.SkipNow()
	// dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{}))
	defer dockerTestTeardown()

	// The client expects this to be sufficiently long
	containerId := "f2e475c0ee1825418a3d4661d39d28bee478f4190d46e1a3984b73ea175c20c3"

	imagesList := `[
		{"Created":1423149832,"Id":"fc24f3225c15b08f8d9f70c1f7148d7fcbf4b41c3acce4b7da25af9371b90501","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-ruby:latest","travis:ruby","travis:default"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423149832,"Id":"08a0d98600afe9d0ca4ca509b1829868cea39dcc75dea1f8dde0dc6325389b45","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-go:latest","travis:go"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423150056,"Id":"570c738990e5859f3b78036f0fb6822fc54dc252f83cdd6d2127e3c1717bbbfd","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-jvm:latest","travis:java","travis:jvm","travis:clojure","travis:groovy","travis:scala"],"Size":1092914295,"VirtualSize":5172004865}
	]`
	dockerTestMux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, imagesList)
	})

	containerCreated := fmt.Sprintf(`{"Id": "%s","Warnings":null}`, containerId)
	dockerTestMux.HandleFunc("/containers/create", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		reqBody, _ := ioutil.ReadAll(r.Body)
		var req containerCreateRequest
		err := json.Unmarshal(reqBody, &req)
		if err != nil {
			t.Errorf("Error decoding docker client container create request: %s", err.Error())
			w.WriteHeader(400)
		} else {
			fmt.Fprintf(w, containerCreated)
		}
	})

	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/start", containerId), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	containerStatus := docker.Container{
		ID: containerId,
		State: docker.State{
			Running: true,
		},
	}
	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/json", containerId), func(w http.ResponseWriter, r *http.Request) {
		containerStatusBytes, _ := json.Marshal(containerStatus)
		w.Write(containerStatusBytes)
	})

	dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Unexpected URL %s", r.URL.String())
		w.WriteHeader(400)
	})

	instance, err := dockerTestProvider.Start(context.TODO(), &StartAttributes{Language: "jvm", Group: ""})
	if err != nil {
		t.Errorf("provider.Start() returned error: %v", err)
	}

	if instance.ID() != "f2e475c:travis:jvm" {
		t.Errorf("Provider returned unexpected ID (\"%s\" != \"f2e475c:travis:jvm\"", instance.ID())
	}
}

func TestDockerStartWithPrivilegedFlag(t *testing.T) {
	t.SkipNow()
	// 	dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
	// 		"PRIVILEGED": "true",
	// 	}))
	defer dockerTestTeardown()

	// The client expects this to be sufficiently long
	containerId := "f2e475c0ee1825418a3d4661d39d28bee478f4190d46e1a3984b73ea175c20c3"

	imagesList := `[
		{"Created":1423149832,"Id":"fc24f3225c15b08f8d9f70c1f7148d7fcbf4b41c3acce4b7da25af9371b90501","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-ruby:latest","travis:ruby","travis:default"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423149832,"Id":"08a0d98600afe9d0ca4ca509b1829868cea39dcc75dea1f8dde0dc6325389b45","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-go:latest","travis:go"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423150056,"Id":"570c738990e5859f3b78036f0fb6822fc54dc252f83cdd6d2127e3c1717bbbfd","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-jvm:latest","travis:java","travis:jvm","travis:clojure","travis:groovy","travis:scala"],"Size":1092914295,"VirtualSize":5172004865}
	]`
	dockerTestMux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, imagesList)
	})

	containerCreated := fmt.Sprintf(`{"Id": "%s","Warnings":null}`, containerId)
	dockerTestMux.HandleFunc("/containers/create", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		reqBody, _ := ioutil.ReadAll(r.Body)
		var req containerCreateRequest
		err := json.Unmarshal(reqBody, &req)
		if err != nil {
			t.Errorf("Error decoding docker client container create request: %s", err.Error())
			w.WriteHeader(400)
		} else if req.HostConfig.Privileged == false {
			t.Errorf("Expected Privileged flag to be true, instead false")
			w.WriteHeader(400)
		} else {
			fmt.Fprintf(w, containerCreated)
		}
	})

	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/start", containerId), func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		reqBody, _ := ioutil.ReadAll(r.Body)
		var req docker.HostConfig
		err := json.Unmarshal(reqBody, &req)
		if err != nil {
			t.Errorf("Error decoding docker client container start request: %s", err.Error())
			w.WriteHeader(400)
		} else if req.Privileged == false {
			t.Errorf("Expected Privileged flag to be true, instead false or not provided")
			w.WriteHeader(400)
		} else {
			w.WriteHeader(204)
		}
	})

	containerStatus := docker.Container{
		ID: containerId,
		State: docker.State{
			Running: true,
		},
	}
	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/json", containerId), func(w http.ResponseWriter, r *http.Request) {
		containerStatusBytes, _ := json.Marshal(containerStatus)
		w.Write(containerStatusBytes)
	})

	dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Unexpected URL %s", r.URL.String())
		w.WriteHeader(400)
	})

	instance, err := dockerTestProvider.Start(context.TODO(), &StartAttributes{Language: "jvm", Group: ""})
	if err != nil {
		t.Errorf("provider.Start() returned error: %v", err)
	}

	if instance.ID() != "f2e475c:travis:jvm" {
		t.Errorf("Provider returned unexpected ID (\"%s\" != \"f2e475c:travis:jvm\"", instance.ID())
	}
}
