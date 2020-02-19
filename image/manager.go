package image

import (
	"bytes"
	gocontext "context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
	"golang.org/x/sync/errgroup"
)

type LXCImage struct {
	Aliases []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"aliases"`
	Size         int       `json:"size"`
	ExpiresAt    time.Time `json:"expires_at"`
	AutoUpdate   bool      `json:"auto_update"`
	LastUsedAt   time.Time `json:"last_used_at"`
	Cached       bool      `json:"cached"`
	UploadedAt   time.Time `json:"uploaded_at"`
	Fingerprint  string    `json:"fingerprint"`
	Architecture string    `json:"architecture"`
	Public       bool      `json:"public"`
	Properties   struct {
		Description string `json:"description"`
	} `json:"properties"`
	Filename  string    `json:"filename"`
	Profiles  []string  `json:"profiles"`
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"`
}

var (
	arch  = runtime.GOARCH
	infra = fmt.Sprintf("lxd-%s", arch)

	tags = []string{"os:linux", "group:stable", "group:edge", "group:dev"}
)

func NewManager(ctx gocontext.Context, selector *APISelector, imagesBaseURL *url.URL) *Manager {
	logger := context.LoggerFromContext(ctx).WithField("self", "image_manager")

	return &Manager{
		selector:      selector,
		logger:        logger,
		imagesBaseURL: imagesBaseURL,
	}
}

type Manager struct {
	selector      *APISelector
	logger        *logrus.Entry
	imagesBaseURL *url.URL
}

func (m *Manager) Load(imageName string) error {
	ok, err := m.Exists(imageName)
	if err != nil {
		return err
	}

	if ok {
		m.logger.WithFields(logrus.Fields{
			"image_name": imageName,
		}).Info("image already present")

		return nil
	}

	url := m.imageUrl(imageName)

	m.logger.WithFields(logrus.Fields{
		"image_name": imageName,
		"url":        url,
	}).Info("downloading image")

	imgPath, err := m.download(url)
	if err != nil {
		return err
	}
	defer os.Remove(imgPath)

	m.logger.WithFields(logrus.Fields{
		"image_name": imageName,
		"image_path": imgPath,
	}).Info("initializing image")

	return m.initialize(imageName, imgPath)
}

func (m *Manager) Exists(imageName string) (bool, error) {
	result, err := m.exec("lxc", "image", "list", "--format=json")
	if err != nil {
		return false, err
	}

	images := []LXCImage{}
	err = json.Unmarshal(result, &images)
	if err != nil {
		return false, err
	}

	for _, img := range images {
		for _, alias := range img.Aliases {
			if alias.Name == imageName {
				return true, nil
			}
		}
	}

	return false, nil
}

func (m *Manager) Update(ctx gocontext.Context) error {
	m.logger.WithFields(logrus.Fields{
		"arch":  arch,
		"infra": infra,
	}).Info("updating lxc images")

	images, err := m.selector.SelectAll(ctx, infra, tags)
	if err != nil {
		return err
	}

	g, _ := errgroup.WithContext(ctx)
	for _, img := range images {
		img := img

		g.Go(func() error {
			m.logger.WithFields(logrus.Fields{
				"image_name": img.Name,
				"infra":      infra,
				"group":      img.Group(),
			}).Info("updating image")

			return m.Load(img.Name)
		})

	}

	return g.Wait()
}

func (m *Manager) Cleanup() error {
	// TODO

	return nil
}

func (m *Manager) download(imageURL string) (string, error) {
	tmpfile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", err
	}

	// TODO: Should we limit max size of the image?
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %s", resp.Status)
	}

	_, err = io.Copy(tmpfile, resp.Body)
	if err != nil {
		return "", err
	}

	err = tmpfile.Close()
	if err != nil {
		return "", err
	}

	return tmpfile.Name(), nil
}

func (m *Manager) initialize(imageName, path string) error {
	_, err := m.exec("lxc", "image", "import", path, fmt.Sprintf("--alias=%s", imageName))
	if err != nil {
		return err
	}

	containerName := fmt.Sprintf("%s-warmup", imageName)
	_, err = m.exec("lxc", "init", imageName, containerName)
	defer func() {
		_, err := m.exec("lxc", "delete", "-f", containerName)
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"error":          err,
				"container_name": containerName,
			}).Error("failed to delete container")
		}
	}()

	return err
}

func (m *Manager) exec(command string, args ...string) ([]byte, error) {
	var out bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &out
	cmd.Stderr = &out

	defer out.Reset()

	m.logger.WithFields(logrus.Fields{
		"command": command,
		"args":    args,
	}).Debug("running lxc command")

	err := cmd.Run()
	if err != nil {
		m.logger.WithFields(logrus.Fields{
			"command": command,
			"args":    args,
			"error":   err,
			"output":  out.String(),
		}).Error("lxc command failed")
	} else {
		m.logger.WithFields(logrus.Fields{
			"output": out.String(),
		}).Debug("lxc command succeeded")
	}

	return out.Bytes(), err
}

// TODO: image URL should be returned by job-board
func (m *Manager) imageUrl(name string) string {
	u := *m.imagesBaseURL
	u.Path = fmt.Sprintf("/%s/%s.tar.gz", arch, name)
	return u.String()
}
