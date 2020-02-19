package backend

import (
	"archive/tar"
	"bytes"
	gocontext "context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dockerapi "github.com/docker/docker/api"
	"github.com/docker/docker/api/server/httputils"
	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
)

var (
	dockerTestMux      *http.ServeMux
	dockerTestProvider *dockerProvider
	dockerTestServer   *httptest.Server
	dockerAPIVersions  = []string{"1.24", "1.27", dockerapi.DefaultVersion}
)

type fakeDockerNumCPUer struct{}

func (nc *fakeDockerNumCPUer) NumCPU() int {
	return 3
}

func dockerTestSetup(t *testing.T, cfg *config.ProviderConfig) (*dockerProvider, error) {
	if cfg == nil {
		cfg = config.ProviderConfigFromMap(map[string]string{})
	}
	defaultDockerNumCPUer = &fakeDockerNumCPUer{}
	dockerTestMux = http.NewServeMux()
	dockerTestServer = httptest.NewServer(dockerTestMux)
	cfg.Set("ENDPOINT", strings.Replace(dockerTestServer.URL, "http", "tcp", 1))
	provider, err := newDockerProvider(cfg)
	if err == nil {
		dockerTestProvider = provider.(*dockerProvider)
	}
	return dockerTestProvider, err
}

func dockerTestTeardown() {
	defaultDockerNumCPUer = &stdlibNumCPUer{}
	if dockerTestServer != nil {
		dockerTestServer.Close()
	}
	dockerTestMux = nil
	dockerTestServer = nil
	dockerTestProvider = nil
}

type containerCreateRequest struct {
	Image      string                     `json:"Image"`
	HostConfig dockercontainer.HostConfig `json:"HostConfig"`
}

func TestDockerProvider_Start(t *testing.T) {
	for _, dockerAPIVersion := range dockerAPIVersions {
		func() {
			_, _ = dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
				"API_VERSION": dockerAPIVersion,
			}))
			defer dockerTestTeardown()

			ctx := context.FromJobID(gocontext.TODO(), 123)
			ctx = context.FromRepository(ctx, "foobar/quux")
			containerName := hostnameFromContext(ctx)

			// The client expects this to be sufficiently long
			containerID := "f2e475c0ee1825418a3d4661d39d28bee478f4190d46e1a3984b73ea175c20c3"

			imagesList := `[
			{"Created":1423149832,"Id":"fc24f3225c15b08f8d9f70c1f7148d7fcbf4b41c3acce4b7da25af9371b90501","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["travisci/ci-garnet:packer-1505167479","travis:ruby","travis:default"],"Size":729301088,"VirtualSize":4808391658},
			{"Created":1423149832,"Id":"08a0d98600afe9d0ca4ca509b1829868cea39dcc75dea1f8dde0dc6325389b45","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["travisci/ci-garnet:packer-1505167479","travis:go"],"Size":729301088,"VirtualSize":4808391658},
			{"Created":1423150056,"Id":"570c738990e5859f3b78036f0fb6822fc54dc252f83cdd6d2127e3c1717bbbfd","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["travisci/ci-amethyst:packer-1504724461","travis:java","travis:jvm","travis:clojure","travis:groovy","travis:scala"],"Size":1092914295,"VirtualSize":5172004865}
		]`
			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/images/json", dockerAPIVersion), func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, imagesList)
			})

			containerCreated := fmt.Sprintf(`{"Id": "%s","Name": "%s","Warnings":null}`, containerID, containerName)
			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/create", dockerAPIVersion), func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var req containerCreateRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				if err != nil {
					t.Errorf("Error decoding docker client container create request: %s", err.Error())
					w.WriteHeader(400)
				} else {
					fmt.Fprint(w, containerCreated)
				}
			})

			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/start", dockerAPIVersion, containerID), func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
			})

			containerStatus := &dockertypes.ContainerJSONBase{
				ID:   containerID,
				Name: containerName,
				State: &dockertypes.ContainerState{
					Running: true,
				},
			}

			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/json", dockerAPIVersion, containerName), func(w http.ResponseWriter, r *http.Request) {
				containerStatusBytes, _ := json.Marshal(containerStatus)
				w.WriteHeader(404)
				_, _ = w.Write(containerStatusBytes)
			})

			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/json", dockerAPIVersion, containerID), func(w http.ResponseWriter, r *http.Request) {
				containerStatusBytes, _ := json.Marshal(containerStatus)
				_, _ = w.Write(containerStatusBytes)
			})

			dockerTestMux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "{\"ApiVersion\":\"%s\"}", dockerAPIVersion)
			})

			dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("Unexpected URL %s", r.URL.String())
				w.WriteHeader(400)
			})

			instance, err := dockerTestProvider.Start(ctx, &StartAttributes{
				Language: "jvm",
				Group:    "",
			})

			if err != nil {
				t.Errorf("provider.Start() returned error: %v", err)
			}

			if instance.ID() != "f2e475c" {
				t.Errorf("Provider returned unexpected ID (\"%s\" != \"f2e475c\"", instance.ID())
			}

			if instance.ImageName() != "travisci/ci-amethyst:packer-1504724461" {
				t.Errorf("Provider returned unexpected ImageName (\"%s\" != \"travisci/ci-amethyst:packer-1504724461\"", instance.ID())
			}
		}()
	}
}

func TestDockerProvider_Start_WithPrivileged(t *testing.T) {
	for _, dockerAPIVersion := range dockerAPIVersions {
		func() {
			_, _ = dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
				"PRIVILEGED":  "true",
				"API_VERSION": dockerAPIVersion,
			}))
			defer dockerTestTeardown()

			// The client expects this to be sufficiently long
			containerID := "f2e475c0ee1825418a3d4661d39d28bee478f4190d46e1a3984b73ea175c20c3"
			ctx := context.FromJobID(gocontext.TODO(), 123)
			ctx = context.FromRepository(ctx, "foobar/quux")
			containerName := hostnameFromContext(ctx)

			imagesList := `[
		{"Created":1423149832,"Id":"fc24f3225c15b08f8d9f70c1f7148d7fcbf4b41c3acce4b7da25af9371b90501","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["travisci/ci-garnet:packer-1505167479","travis:ruby","travis:default"],"Size":729301088,"VirtualSize":4808391658},
			{"Created":1423149832,"Id":"08a0d98600afe9d0ca4ca509b1829868cea39dcc75dea1f8dde0dc6325389b45","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["travisci/ci-garnet:packer-1505167479","travis:go"],"Size":729301088,"VirtualSize":4808391658},
			{"Created":1423150056,"Id":"570c738990e5859f3b78036f0fb6822fc54dc252f83cdd6d2127e3c1717bbbfd","Labels":null,"ParentId":"2b412eda4314d97ff8a90d2f8c1b65677399723d6ecc4950f4e1247a5c2193c0","RepoDigests":[],"RepoTags":["travisci/ci-amethyst:packer-1504724461","travis:java","travis:jvm","travis:clojure","travis:groovy","travis:scala"],"Size":1092914295,"VirtualSize":5172004865}
	]`
			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/images/json", dockerAPIVersion), func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, imagesList)
			})

			containerCreated := fmt.Sprintf(`{"Id": "%s","Warnings":null}`, containerID)
			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/create", dockerAPIVersion), func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var req containerCreateRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				if err != nil {
					t.Errorf("Error decoding docker client container create request: %s", err.Error())
					w.WriteHeader(400)
				} else if !req.HostConfig.Privileged {
					t.Errorf("Expected Privileged flag to be true, instead false")
					w.WriteHeader(400)
				} else {
					fmt.Fprint(w, containerCreated)
				}
			})

			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/start", dockerAPIVersion, containerID), func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
			})

			containerStatus := &dockertypes.ContainerJSONBase{
				ID:   containerID,
				Name: containerName,
				State: &dockertypes.ContainerState{
					Running: true,
				},
			}
			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/json", dockerAPIVersion, containerID), func(w http.ResponseWriter, r *http.Request) {
				containerStatusBytes, _ := json.Marshal(containerStatus)
				_, _ = w.Write(containerStatusBytes)
			})

			dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/json", dockerAPIVersion, containerName), func(w http.ResponseWriter, r *http.Request) {
				containerStatusBytes, _ := json.Marshal(containerStatus)
				w.WriteHeader(404)
				_, _ = w.Write(containerStatusBytes)
			})

			dockerTestMux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "{\"ApiVersion\":\"%s\"}", dockerAPIVersion)
			})

			dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("Unexpected URL %s", r.URL.String())
				w.WriteHeader(400)
			})

			instance, err := dockerTestProvider.Start(ctx, &StartAttributes{Language: "jvm", Group: ""})
			if err != nil {
				t.Errorf("provider.Start() returned error: %v", err)
			}

			if instance.ID() != "f2e475c" {
				t.Errorf("Provider returned unexpected ID (\"%s\" != \"f2e475c\"", instance.ID())
			}

			if instance.ImageName() != "travisci/ci-amethyst:packer-1504724461" {
				t.Errorf("Provider returned unexpected ImageName (\"%s\" != \"travisci/ci-amethyst:packer-1504724461\"", instance.ImageName())
			}
		}()
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
	provider, err := dockerTestSetup(t, nil)

	assert.Nil(t, err)
	assert.NotNil(t, provider)
	assert.NotNil(t, provider.client)
	assert.False(t, provider.runNative)
	assert.False(t, provider.runPrivileged)
	assert.Equal(t, uint64(1024*1024*1024*4), provider.runMemory)
	assert.Equal(t, 3, len(provider.cpuSets))
	assert.Equal(t, []string{"/sbin/init"}, provider.runCmd)
	assert.Equal(t, uint(2), provider.runCPUs)
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
	_, _ = dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
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
	assert.Equal(t, "strconv.ParseInt: parsing \"fafafaf\": invalid syntax", err.Error())
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

func TestNewDockerProvider_WithInspectInterval(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"INSPECT_INTERVAL": "1500ms",
	}))
	defer dockerTestTeardown()

	assert.Nil(t, err)
	assert.Equal(t, 1500*time.Millisecond, provider.inspectInterval)
}

func TestNewDockerProvider_WithInvalidInspectInterval(t *testing.T) {
	provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"INSPECT_INTERVAL": "mraaaaaaa",
	}))
	defer dockerTestTeardown()

	assert.NotNil(t, err)
	assert.Equal(t, "time: invalid duration mraaaaaaa", err.Error())
	assert.Nil(t, provider)
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
	for _, v := range []uint{4, 1, 0} {
		func(cpus uint) {
			provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
				"CPUS": fmt.Sprintf("%d", cpus),
			}))
			defer dockerTestTeardown()

			assert.Nil(t, err)
			assert.Equal(t, cpus, provider.runCPUs)
		}(v)
	}
}

func TestDockerProvider_Setup(t *testing.T) {
	provider, _ := dockerTestSetup(t, nil)
	_ = provider.Setup(gocontext.TODO())
}

func TestDockerInstance_UploadScript_WithNative(t *testing.T) {
	for _, dockerAPIVersion := range dockerAPIVersions {
		provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
			"NATIVE":      "true",
			"API_VERSION": dockerAPIVersion,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, provider)

		instance := &dockerInstance{
			client:    provider.client,
			provider:  provider,
			runNative: provider.runNative,
			container: &dockertypes.ContainerJSON{
				ContainerJSONBase: &dockertypes.ContainerJSONBase{ID: "beabebabafabafaba0000"},
			},
			imageName:    "fafafaf",
			startBooting: time.Now(),
		}

		script := []byte("#!/bin/bash\necho hai\n")
		scriptUploaded := false

		dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/archive", dockerAPIVersion, instance.container.ID),
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
				assert.Equal(t, err, io.EOF)
				assert.Equal(t, buf, script)

				scriptUploaded = true
			})
		dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			t.Logf("got: %s %s", req.Method, req.URL.Path)
		})

		err = instance.UploadScript(gocontext.TODO(), script)
		assert.Nil(t, err)
		assert.True(t, scriptUploaded)
	}
}

func TestDockerInstance_RunScript_WithNative(t *testing.T) {
	for _, dockerAPIVersion := range dockerAPIVersions {
		provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
			"NATIVE":      "true",
			"API_VERSION": dockerAPIVersion,
		}))

		assert.Nil(t, err)
		assert.NotNil(t, provider)

		containerID := "beabebabafabafaba0000"
		instance := &dockerInstance{
			client:    provider.client,
			provider:  provider,
			runNative: provider.runNative,
			container: &dockertypes.ContainerJSON{
				ContainerJSONBase: &dockertypes.ContainerJSONBase{ID: containerID},
			},
			imageName:    "fafafaf",
			startBooting: time.Now(),
		}

		scriptRun := false
		writer := &bytes.Buffer{}

		dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/exec", dockerAPIVersion, containerID), func(w http.ResponseWriter, req *http.Request) {
			if req.Method != "POST" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"ID":"ffbada"}`)
		})

		dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/exec/ffbada/start", dockerAPIVersion), func(w http.ResponseWriter, req *http.Request) {
			inStream, outStream, err := httputils.HijackConnection(w)
			if err != nil {
				t.Logf("failed to hijack connection: %v", err)
				return
			}

			defer httputils.CloseStreams(inStream, outStream)

			if _, ok := req.Header["Upgrade"]; ok {
				fmt.Fprintf(outStream, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n")
			} else {
				fmt.Fprintf(outStream, "HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.raw-stream\r\n")
			}

			err = w.Header().WriteSubset(outStream, nil)
			if err != nil {
				t.Logf("failed to copy headers: %v", err)
				return
			}

			fmt.Fprintf(outStream, "\r\n")
			fmt.Fprintf(outStream, "hello bye\n")
		})

		dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/exec/ffbada/json", dockerAPIVersion), func(w http.ResponseWriter, req *http.Request) {
			scriptRun = true
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"ExitCode":0,"Running":false}`)
		})

		dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			t.Logf("got: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
		})

		ctx, cancel := gocontext.WithTimeout(gocontext.Background(), 5*time.Second)
		defer cancel()
		res, err := instance.RunScript(ctx, writer)
		assert.NotNil(t, res)
		assert.Nil(t, err)
		assert.True(t, scriptRun)
		assert.True(t, res.Completed)
	}
}

func TestDockerInstance_Stop(t *testing.T) {
	for _, dockerAPIVersion := range dockerAPIVersions {
		provider, err := dockerTestSetup(t, config.ProviderConfigFromMap(map[string]string{
			"API_VERSION": dockerAPIVersion,
		}))
		assert.Nil(t, err)
		assert.NotNil(t, provider)

		containerID := "beabebabafabafaba0000"
		instance := &dockerInstance{
			client:    provider.client,
			provider:  provider,
			runNative: provider.runNative,
			container: &dockertypes.ContainerJSON{
				ContainerJSONBase: &dockertypes.ContainerJSONBase{
					ID: containerID,
					HostConfig: &dockercontainer.HostConfig{
						Resources: dockercontainer.Resources{
							CpusetCpus: "0,1",
						},
					},
				},
			},
			imageName:    "fafafaf",
			startBooting: time.Now(),
		}

		wasDeleted := false

		dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s/stop", dockerAPIVersion, containerID), func(w http.ResponseWriter, req *http.Request) {
			if req.Method != "POST" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			w.WriteHeader(http.StatusOK)
		})

		dockerTestMux.HandleFunc(fmt.Sprintf("/v%s/containers/%s", dockerAPIVersion, containerID), func(w http.ResponseWriter, req *http.Request) {
			if req.Method != "DELETE" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			wasDeleted = true
			w.WriteHeader(http.StatusNoContent)
		})

		dockerTestMux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			t.Logf("got: %s %s", req.Method, req.URL.Path)
		})

		err = instance.Stop(gocontext.TODO())
		assert.Nil(t, err)
		assert.True(t, wasDeleted)
	}
}

func TestDockerInstance_StartupDuration(t *testing.T) {
	provider, err := dockerTestSetup(t, nil)

	assert.Nil(t, err)
	assert.NotNil(t, provider)

	now := time.Now()
	containerID := "beabebabafabafaba0000"

	instance := &dockerInstance{
		client:    provider.client,
		provider:  provider,
		runNative: provider.runNative,
		container: &dockertypes.ContainerJSON{
			ContainerJSONBase: &dockertypes.ContainerJSONBase{
				ID:      containerID,
				Created: now.Add(-3 * time.Second).Format(time.RFC3339Nano),
			},
		},
		imageName:    "fafafaf",
		startBooting: now,
	}

	assert.True(t, instance.StartupDuration() > 0)

	instance.container = nil
	assert.Equal(t, zeroDuration, instance.StartupDuration())
}

func TestDockerInstance_ID(t *testing.T) {
	provider, err := dockerTestSetup(t, nil)

	assert.Nil(t, err)
	assert.NotNil(t, provider)

	now := time.Now()
	containerID := "beabebabafabafaba0000"

	instance := &dockerInstance{
		client:    provider.client,
		provider:  provider,
		runNative: provider.runNative,
		container: &dockertypes.ContainerJSON{
			ContainerJSONBase: &dockertypes.ContainerJSONBase{ID: containerID},
		},
		imageName:    "fafafaf",
		startBooting: now,
	}

	assert.Equal(t, "beabeba", instance.ID())
	assert.Equal(t, "fafafaf", instance.ImageName())

	instance.container = nil
	assert.Equal(t, "{unidentified}", instance.ID())
}
