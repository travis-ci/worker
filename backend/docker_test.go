package backend

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/fsouza/go-dockerclient"
	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/config"
	"golang.org/x/net/context"
)

var (
	dockerTestMux      *http.ServeMux
	dockerTestProvider *dockerProvider
	dockerTestServer   *httptest.Server
)

type fakeDockerNumCPUer struct{}

func (nc *fakeDockerNumCPUer) NumCPU() int {
	return 3
}

func dockerTestSetup(t *testing.T, cfg *config.ProviderConfig) (*dockerProvider, error) {
	defaultDockerNumCPUer = &fakeDockerNumCPUer{}
	dockerTestMux = http.NewServeMux()
	dockerTestServer = httptest.NewServer(dockerTestMux)
	cfg.Set("ENDPOINT", dockerTestServer.URL)
	provider, err := newDockerProvider(cfg)
	if err == nil {
		dockerTestProvider = provider.(*dockerProvider)
	}
	return dockerTestProvider, err
}

func dockerTestTeardown() {
	defaultDockerNumCPUer = &stdlibNumCPUer{}
	dockerTestServer.Close()
	dockerTestMux = nil
	dockerTestServer = nil
	dockerTestProvider = nil
}

type containerCreateRequest struct {
	Image      string            `json:"Image"`
	HostConfig docker.HostConfig `json:"HostConfig"`
}

func TestDockerProvider_Start(t *testing.T) {
	dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{}))
	defer dockerTestTeardown()

	// The client expects this to be sufficiently long
	containerID := "f2e475c0ee1825418a3d4661d39d28bee478f4190d46e1a3984b73ea175c20c3"

	imagesList := `[
		{"Created":1423149832,"Id":"fc24f3225c15b08f8d9f70c1f7148d7fcbf4b41c3acce4b7da25af9371b90501","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-ruby:latest","travis:ruby","travis:default"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423149832,"Id":"08a0d98600afe9d0ca4ca509b1829868cea39dcc75dea1f8dde0dc6325389b45","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-go:latest","travis:go"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423150056,"Id":"570c738990e5859f3b78036f0fb6822fc54dc252f83cdd6d2127e3c1717bbbfd","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-jvm:latest","travis:java","travis:jvm","travis:clojure","travis:groovy","travis:scala"],"Size":1092914295,"VirtualSize":5172004865}
	]`
	dockerTestMux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, imagesList)
	})

	containerCreated := fmt.Sprintf(`{"Id": "%s","Warnings":null}`, containerID)
	dockerTestMux.HandleFunc("/containers/create", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req containerCreateRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			t.Errorf("Error decoding docker client container create request: %s", err.Error())
			w.WriteHeader(400)
		} else {
			fmt.Fprintf(w, containerCreated)
		}
	})

	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/start", containerID), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	containerStatus := docker.Container{
		ID: containerID,
		State: docker.State{
			Running: true,
		},
	}
	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/json", containerID), func(w http.ResponseWriter, r *http.Request) {
		containerStatusBytes, _ := json.Marshal(containerStatus)
		w.Write(containerStatusBytes)
	})

	dockerTestMux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ApiVersion":"1.24"}`)
	})

	dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Unexpected URL %s", r.URL.String())
		w.WriteHeader(400)
	})

	instance, err := dockerTestProvider.Start(context.TODO(), &StartAttributes{
		Language: "jvm",
		Group:    "",
	})

	if err != nil {
		t.Errorf("provider.Start() returned error: %v", err)
	}

	if instance.ID() != "f2e475c:travis:jvm" {
		t.Errorf("Provider returned unexpected ID (\"%s\" != \"f2e475c:travis:jvm\"", instance.ID())
	}
}

func TestDockerProvider_Start_WithPrivileged(t *testing.T) {
	dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"PRIVILEGED": "true",
	}))
	defer dockerTestTeardown()

	// The client expects this to be sufficiently long
	containerID := "f2e475c0ee1825418a3d4661d39d28bee478f4190d46e1a3984b73ea175c20c3"

	imagesList := `[
		{"Created":1423149832,"Id":"fc24f3225c15b08f8d9f70c1f7148d7fcbf4b41c3acce4b7da25af9371b90501","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-ruby:latest","travis:ruby","travis:default"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423149832,"Id":"08a0d98600afe9d0ca4ca509b1829868cea39dcc75dea1f8dde0dc6325389b45","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-go:latest","travis:go"],"Size":729301088,"VirtualSize":4808391658},
		{"Created":1423150056,"Id":"570c738990e5859f3b78036f0fb6822fc54dc252f83cdd6d2127e3c1717bbbfd","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["quay.io/travisci/travis-jvm:latest","travis:java","travis:jvm","travis:clojure","travis:groovy","travis:scala"],"Size":1092914295,"VirtualSize":5172004865}
	]`
	dockerTestMux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, imagesList)
	})

	containerCreated := fmt.Sprintf(`{"Id": "%s","Warnings":null}`, containerID)
	dockerTestMux.HandleFunc("/containers/create", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req containerCreateRequest
		err := json.NewDecoder(r.Body).Decode(&req)
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

	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/start", containerID), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	containerStatus := docker.Container{
		ID: containerID,
		State: docker.State{
			Running: true,
		},
	}
	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/json", containerID), func(w http.ResponseWriter, r *http.Request) {
		containerStatusBytes, _ := json.Marshal(containerStatus)
		w.Write(containerStatusBytes)
	})

	dockerTestMux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"ApiVersion":"1.24"}`)
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

func TestNewDockerProvider_WithInvalidPrivileged(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"PRIVILEGED": "fafafaf",
	}))
	assert.NotNil(t, err)
	assert.Nil(t, provider)
}

func TestNewDockerProvider_WithMissingEndpoint(t *testing.T) {
	provider, err := newDockerProvider(config.ProviderConfigFromMap(map[string]string{}))
	assert.NotNil(t, err)
	assert.Nil(t, provider)
}

func TestNewDockerProvider_WithDockerHost(t *testing.T) {
	provider, err := newDockerProvider(config.ProviderConfigFromMap(map[string]string{
		"HOST": "tcp://fleeflahflew.example.com:8080",
	}))
	assert.Nil(t, err)
	assert.NotNil(t, provider)
}

func TestNewDockerProvider_WithRequiredConfig(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{}))

	assert.Nil(t, err)
	assert.NotNil(t, provider)
	assert.NotNil(t, provider.client)
	assert.False(t, provider.runNative)
	assert.False(t, provider.runPrivileged)
	assert.Equal(t, uint64(1024*1024*1024*4), provider.runMemory)
	assert.Equal(t, 3, len(provider.cpuSets))
	assert.Equal(t, []string{"/sbin/init"}, provider.runCmd)
	assert.Equal(t, 2, provider.runCPUs)
}

func TestNewDockerProvider_WithCertPath(t *testing.T) {
	certPath := fmt.Sprintf("/%v/secret/nonexistent/dir", time.Now().UTC().UnixNano())
	provider, err := newDockerProvider(config.ProviderConfigFromMap(map[string]string{
		"HOST":      "tcp://fleeflahflew.example.com:8080",
		"CERT_PATH": certPath,
	}))
	assert.NotNil(t, err)
	assert.Nil(t, provider)
	assert.Equal(t, err.(*os.PathError).Op, "open")
	assert.Equal(t, err.(*os.PathError).Path, fmt.Sprintf("%s/cert.pem", certPath))
}

func TestNewDockerProvider_WithNative(t *testing.T) {
	provider, err := newDockerProvider(config.ProviderConfigFromMap(map[string]string{
		"HOST":   "tcp://fleeflahflew.example.com:8080",
		"NATIVE": "1",
	}))
	assert.Nil(t, err)
	assert.NotNil(t, provider)
	assert.True(t, provider.(*dockerProvider).runNative)

	provider, err = newDockerProvider(config.ProviderConfigFromMap(map[string]string{
		"HOST":   "tcp://fleeflahflew.example.com:8080",
		"NATIVE": "wat",
	}))

	assert.NotNil(t, err)
	assert.Nil(t, provider)
}

func TestNewDockerProvider_WithCPUSetSize(t *testing.T) {
	dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"NATIVE":       "1",
		"CPU_SET_SIZE": "16",
	}))
	defer dockerTestTeardown()

	assert.Equal(t, 16, len(dockerTestProvider.cpuSets))
}

func TestNewDockerProvider_WithInvalidCPUSetSize(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"NATIVE":       "1",
		"CPU_SET_SIZE": "fafafaf",
	}))
	defer dockerTestTeardown()

	assert.NotNil(t, err)
	assert.Nil(t, provider)
}

func TestNewDockerProvider_WithCPUSetSizeLessThan2(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"NATIVE":       "1",
		"CPU_SET_SIZE": "1",
	}))
	defer dockerTestTeardown()

	assert.Nil(t, err)
	assert.Equal(t, 2, len(provider.cpuSets))
}

func TestNewDockerProvider_WithCMD(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"CMD": "/bin/bash /fancy-docker-init-thing",
	}))
	defer dockerTestTeardown()

	assert.Nil(t, err)
	assert.Equal(t, []string{"/bin/bash", "/fancy-docker-init-thing"}, provider.runCmd)
}

func TestNewDockerProvider_WithMemory(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"MEMORY": "99MB",
	}))
	defer dockerTestTeardown()

	assert.Nil(t, err)
	assert.Equal(t, uint64(0x5e69ec0), provider.runMemory)
}

func TestNewDockerProvider_WithCPUs(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"CPUS": "4",
	}))
	defer dockerTestTeardown()

	assert.Nil(t, err)
	assert.Equal(t, 4, provider.runCPUs)
}

func TestDockerProvider_Setup(t *testing.T) {
	provider, _ := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{}))
	provider.Setup(nil)
}

func TestDockerInstance_UploadScript_WithNative(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"NATIVE": "true",
	}))

	assert.Nil(t, err)
	assert.NotNil(t, provider)

	instance := &dockerInstance{
		client:       provider.client,
		provider:     provider,
		runNative:    provider.runNative,
		container:    &docker.Container{ID: "beabebabafabafaba0000"},
		imageName:    "fafafaf",
		startBooting: time.Now(),
	}

	script := []byte("#!/bin/bash\necho hai\n")
	scriptUploaded := false

	dockerTestMux.HandleFunc(fmt.Sprintf("/containers/%s/archive", instance.container.ID),
		func(w http.ResponseWriter, req *http.Request) {
			assert.Nil(t, err)
			assert.Equal(t, "PUT", req.Method)

			tr := tar.NewReader(req.Body)
			hdr, err := tr.Next()

			assert.Nil(t, err)
			assert.Equal(t, "/home/travis/build.sh", hdr.Name)
			assert.Equal(t, int64(len(script)), hdr.Size)
			assert.Equal(t, int64(0755), hdr.Mode)

			buf := make([]byte, hdr.Size)
			_, err = tr.Read(buf)
			assert.Nil(t, err)
			assert.Equal(t, buf, script)

			scriptUploaded = true
		})

	err = instance.UploadScript(context.TODO(), script)
	assert.Nil(t, err)
	assert.True(t, scriptUploaded)
}
