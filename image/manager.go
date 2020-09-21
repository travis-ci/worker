package image

import (
	gocontext "context"
	"fmt"
	"net/url"
	"runtime"

	lxd "github.com/lxc/lxd/client"
	lxdapi "github.com/lxc/lxd/shared/api"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
	"golang.org/x/sync/errgroup"
)

var (
	arch  = runtime.GOARCH
	infra = fmt.Sprintf("lxd-%s", arch)

	tags   = []string{"os:linux"}
	groups = []string{"group:stable", "group:edge", "group:dev"}
)

func NewManager(ctx gocontext.Context, selector *APISelector, imagesServerURL *url.URL) (*Manager, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "image_manager")

	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return nil, err
	}

	return &Manager{
		client:          client,
		selector:        selector,
		logger:          logger,
		imagesServerURL: imagesServerURL,
	}, nil
}

type Manager struct {
	client          lxd.ContainerServer
	selector        *APISelector
	logger          *logrus.Entry
	imagesServerURL *url.URL
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

	imageURL := m.imageUrl(imageName)

	m.logger.WithFields(logrus.Fields{
		"image_name": imageName,
		"image_url":  imageURL,
	}).Info("importing image")

	return m.importImage(imageName, imageURL)
}

func (m *Manager) Exists(imageName string) (bool, error) {
	images, err := m.client.GetImages()
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

	images := []*apiSelectorImageRef{}

	for _, group := range groups {
		imagesGroup, err := m.selector.SelectAll(ctx, infra, append(tags, group))
		if err != nil {
			return err
		}

		images = append(images, imagesGroup...)
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

func (m *Manager) importImage(imageName, imgURL string) error {
	op, err := m.client.CreateImage(
		lxdapi.ImagesPost{
			Filename: imageName,
			Source: &lxdapi.ImagesPostSource{
				Type: "url",
				URL:  imgURL,
			},
			Aliases: []lxdapi.ImageAlias{
				lxdapi.ImageAlias{Name: imageName},
			},
		}, nil,
	)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	alias, _, err := m.client.GetImageAlias(imageName)
	if err != nil {
		return err
	}

	image, _, err := m.client.GetImage(alias.Target)
	if err != nil {
		return err
	}

	containerName := fmt.Sprintf("%s-warmup", imageName)

	rop, err := m.client.CreateContainerFromImage(
		m.client,
		*image,
		lxdapi.ContainersPost{
			Name: containerName,
		},
	)
	if err != nil {
		return err
	}

	err = rop.Wait()
	if err != nil {
		return err
	}

	defer func() {
		op, err := m.client.DeleteContainer(containerName)
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"error":          err,
				"container_name": containerName,
			}).Error("failed to delete container")
			return
		}

		err = op.Wait()
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"error":          err,
				"container_name": containerName,
			}).Error("failed to delete container")
			return
		}
	}()

	return err
}

func (m *Manager) imageUrl(name string) string {
	u := *m.imagesServerURL
	u.Path = fmt.Sprintf("/images/travis/%s", name)
	return u.String()
}
