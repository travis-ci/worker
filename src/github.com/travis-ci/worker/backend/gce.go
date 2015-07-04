package backend

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/pborman/uuid"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/compute/v1"
)

const (
	defaultGCEZone        = "us-central1-a"
	defaultGCEMachineType = "n1-standard-2"
	defaultGCENetwork     = "default"
	defaultGCEDiskSize    = int64(20)
	gceImagesFilter       = "name eq ^travis-ci-%s.+"
	gceStartupScript      = `#!/usr/bin/env bash
cat > ~travis/.ssh/authorized_keys <<EOF
%s
EOF
`
)

var (
	gceHelp = fmt.Sprintf(`
             PROJECT_ID - [REQUIRED] GCE project id
           ACCOUNT_JSON - [REQUIRED] account JSON config
           SSH_KEY_PATH - [REQUIRED] path to ssh key used to access job vms
     SSH_KEY_PASSPHRASE - [REQUIRED] passphrase for ssh key given as ssh_key_path
                   ZONE - zone name (default %q)
           MACHINE_TYPE - machine name (default %q)
                NETWORK - machine name (default %q)
              DISK_SIZE - disk size in GB (default %v)

`, defaultGCEZone, defaultGCEMachineType, defaultGCENetwork, defaultGCEDiskSize)
	gceMissingIPAddressError = fmt.Errorf("no IP address found")
)

func init() {
	config.SetProviderHelp("GCE", gceHelp)
}

type gceOpError struct {
	Err *compute.OperationError
}

func (oe *gceOpError) Error() string {
	errStrs := []string{}
	for _, err := range oe.Err.Errors {
		errStrs = append(errStrs, fmt.Sprintf("code=%s location=%s message=%s",
			err.Code, err.Location, err.Message))
	}

	return strings.Join(errStrs, ", ")
}

type gceAccountJSON struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

type GCEProvider struct {
	client    *compute.Service
	projectID string
	ic        *gceInstanceConfig
}

type gceInstanceConfig struct {
	MachineType      *compute.MachineType
	Zone             *compute.Zone
	Network          *compute.Network
	DiskType         string
	DiskSize         int64
	SSHKeyPath       string
	SSHKeyPassphrase string
}

type GCEInstance struct {
	client   *compute.Service
	provider *GCEProvider
	instance *compute.Instance
	ic       *gceInstanceConfig

	authUser string
	authKey  *rsa.PrivateKey

	projectID string
	imageName string
}

func NewGCEProvider(cfg *config.ProviderConfig) (*GCEProvider, error) {
	client, err := buildGoogleComputeService(cfg)
	if err != nil {
		return nil, err
	}

	if !cfg.IsSet("PROJECT_ID") {
		return nil, fmt.Errorf("mising PROJECT_ID")
	}

	projectID := cfg.Get("PROJECT_ID")

	if !cfg.IsSet("SSH_KEY_PATH") {
		return nil, fmt.Errorf("expected SSH_KEY_PATH config key")
	}

	sshKeyPath := cfg.Get("SSH_KEY_PATH")

	if !cfg.IsSet("SSH_KEY_PASSPHRASE") {
		return nil, fmt.Errorf("expected SSH_KEY_PASSPHRASE config key")
	}

	sshKeyPassphrase := cfg.Get("SSH_KEY_PASSPHRASE")

	zoneName := defaultGCEZone
	if cfg.IsSet("ZONE") {
		zoneName = cfg.Get("ZONE")
	}

	zone, err := client.Zones.Get(projectID, zoneName).Do()
	if err != nil {
		return nil, err
	}

	mtName := defaultGCEMachineType
	if cfg.IsSet("MACHINE_TYPE") {
		mtName = cfg.Get("MACHINE_TYPE")
	}

	mt, err := client.MachineTypes.Get(projectID, zone.Name, mtName).Do()
	if err != nil {
		return nil, err
	}

	nwName := defaultGCENetwork
	if cfg.IsSet("NETWORK") {
		nwName = cfg.Get("NETWORK")
	}

	nw, err := client.Networks.Get(projectID, nwName).Do()
	if err != nil {
		return nil, err
	}

	diskSize := defaultGCEDiskSize
	if cfg.IsSet("DISK_SIZE") {
		ds, err := strconv.ParseInt(cfg.Get("DISK_SIZE"), 10, 64)
		if err == nil {
			diskSize = ds
		}
	}

	return &GCEProvider{
		client:    client,
		projectID: projectID,
		ic: &gceInstanceConfig{
			MachineType: mt,
			Zone:        zone,
			Network:     nw,
			// DiskType:         fmt.Sprintf("zones/%s/diskTypes/local-ssd", zone.Name),
			DiskType:         fmt.Sprintf("zones/%s/diskTypes/pd-standard", zone.Name),
			DiskSize:         diskSize,
			SSHKeyPath:       sshKeyPath,
			SSHKeyPassphrase: sshKeyPassphrase,
		},
	}, nil
}

func buildGoogleComputeService(cfg *config.ProviderConfig) (*compute.Service, error) {
	if !cfg.IsSet("ACCOUNT_JSON") {
		return nil, fmt.Errorf("missing ACCOUNT_JSON")
	}

	a, err := loadGoogleAccountJSON(cfg.Get("ACCOUNT_JSON"))
	if err != nil {
		return nil, err
	}

	config := jwt.Config{
		Email:      a.ClientEmail,
		PrivateKey: []byte(a.PrivateKey),
		Scopes: []string{
			compute.DevstorageFullControlScope,
			compute.ComputeScope,
		},
		TokenURL: "https://accounts.google.com/o/oauth2/token",
	}
	return compute.New(config.Client(oauth2.NoContext))
}

func loadGoogleAccountJSON(filename string) (*gceAccountJSON, error) {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	a := &gceAccountJSON{}
	err = json.Unmarshal(bytes, a)
	return a, err
}

func (p *GCEProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx)
	image, err := p.imageForLanguage(startAttributes.Language)
	if err != nil {
		return nil, err
	}

	authKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	pubKey, err := ssh.NewPublicKey(authKey)
	if err != nil {
		return nil, err
	}

	inst := &compute.Instance{
		Description: fmt.Sprintf("Travis CI %s test VM", startAttributes.Language),
		Disks: []*compute.AttachedDisk{
			&compute.AttachedDisk{
				Type:       "PERSISTENT",
				Mode:       "READ_WRITE",
				Boot:       true,
				AutoDelete: true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: image.SelfLink,
					DiskType:    p.ic.DiskType,
					DiskSizeGb:  p.ic.DiskSize,
				},
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible: true,
		},
		MachineType: p.ic.MachineType.SelfLink,
		Name:        fmt.Sprintf("testing-gce-%s", uuid.NewUUID()),
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				&compute.MetadataItems{
					Key:   "startup-script",
					Value: fmt.Sprintf(gceStartupScript, string(ssh.MarshalAuthorizedKey(pubKey))),
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			&compute.NetworkInterface{
				AccessConfigs: []*compute.AccessConfig{
					&compute.AccessConfig{
						Name: "AccessConfig brought to you by travis-worker",
						Type: "ONE_TO_ONE_NAT",
					},
				},
				Network: p.ic.Network.SelfLink,
			},
		},
		ServiceAccounts: []*compute.ServiceAccount{
			&compute.ServiceAccount{
				Email: "default",
				Scopes: []string{
					"https://www.googleapis.com/auth/userinfo.email",
					compute.DevstorageFullControlScope,
					compute.ComputeScope,
				},
			},
		},
		Tags: &compute.Tags{
			Items: []string{
				"testing",
				startAttributes.Language,
			},
		},
	}

	logger.WithFields(logrus.Fields{
		"instance": inst,
	}).Debug("inserting instance")
	op, err := p.client.Instances.Insert(p.projectID, p.ic.Zone.Name, inst).Do()
	if err != nil {
		return nil, err
	}

	startBooting := time.Now()

	instanceReady := make(chan *compute.Instance)
	errChan := make(chan error)
	go func() {
		for {
			newOp, err := p.client.ZoneOperations.Get(p.projectID, p.ic.Zone.Name, op.Name).Do()
			if err != nil {
				errChan <- err
				return
			}

			if newOp.Status == "DONE" {
				if newOp.Error != nil {
					errChan <- &gceOpError{Err: newOp.Error}
					return
				}

				instanceReady <- inst
				return
			}
		}
	}()

	select {
	case inst := <-instanceReady:
		metrics.TimeSince("worker.vm.provider.gce.boot", startBooting)
		return &GCEInstance{
			client:   p.client,
			provider: p,
			instance: inst,
			ic:       p.ic,

			authUser: "travis",
			authKey:  authKey,

			projectID: p.projectID,
			imageName: image.Name,
		}, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.gce.boot.timeout")
		}
		return nil, ctx.Err()
	}
}

func (p *GCEProvider) imageForLanguage(language string) (*compute.Image, error) {
	images, err := p.client.Images.List(p.projectID).Filter(fmt.Sprintf(gceImagesFilter, language)).Do()
	if err != nil {
		return nil, err
	}

	if len(images.Items) == 0 {
		return nil, fmt.Errorf("no image found with language %s", language)
	}

	imagesByName := map[string]*compute.Image{}
	imageNames := []string{}
	for _, image := range images.Items {
		imagesByName[image.Name] = image
		imageNames = append(imageNames, image.Name)
	}

	sort.Strings(imageNames)

	return imagesByName[imageNames[len(imageNames)-1]], nil
}

func (i *GCEInstance) sshClient() (*ssh.Client, error) {
	/*
		file, err := ioutil.ReadFile(i.ic.SSHKeyPath)
		if err != nil {
			return nil, err
		}

		block, _ := pem.Decode(file)
		if block == nil {
			return nil, fmt.Errorf("ssh key does not contain a valid PEM block")
		}

		der, err := x509.DecryptPEMBlock(block, []byte(i.ic.SSHKeyPassphrase))
		if err != nil {
			return nil, err
		}

		key, err := x509.ParsePKCS1PrivateKey(der)
		if err != nil {
			return nil, err
		}

		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			return nil, err
		}
	*/

	i.refreshInstance()

	ipAddr := i.getIP()
	if ipAddr == "" {
		return nil, gceMissingIPAddressError
	}

	// FIXME: i.authKey is *rsa.PrivateKey, but NewSignerFromKey rejects it??
	ks, err := ssh.NewSignerFromKey(i.authKey)
	if err != nil {
		return nil, err
	}

	return ssh.Dial("tcp", fmt.Sprintf("%s:22", ipAddr), &ssh.ClientConfig{
		User: i.authUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(ks),
		},
	})
}

func (i *GCEInstance) getIP() string {
	for _, ni := range i.instance.NetworkInterfaces {
		if ni.AccessConfigs == nil {
			continue
		}

		for _, ac := range ni.AccessConfigs {
			if ac.NatIP != "" {
				return ac.NatIP
			}
		}
	}

	return ""
}

func (i *GCEInstance) refreshInstance() error {
	inst, err := i.client.Instances.Get(i.projectID, i.ic.Zone.Name, i.instance.Name).Do()
	if err != nil {
		return err
	}

	i.instance = inst
	return nil
}

func (i *GCEInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	client, err := i.sshClient()
	if err != nil {
		return err
	}
	defer client.Close()

	sftp, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftp.Close()

	_, err = sftp.Lstat("build.sh")
	if err == nil {
		return ErrStaleVM
	}

	f, err := sftp.Create("build.sh")
	if err != nil {
		return err
	}

	if _, err := f.Write(script); err != nil {
		return err
	}

	return nil
}

func (i *GCEInstance) RunScript(ctx gocontext.Context, output io.WriteCloser) (*RunResult, error) {
	client, err := i.sshClient()
	if err != nil {
		return &RunResult{Completed: false}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &RunResult{Completed: false}, err
	}
	defer session.Close()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Run("bash ~/build.sh")
	if err == nil {
		return &RunResult{Completed: true, ExitCode: 0}, nil
	}

	switch err := err.(type) {
	case *ssh.ExitError:
		return &RunResult{Completed: true, ExitCode: uint8(err.ExitStatus())}, nil
	default:
		return &RunResult{Completed: false}, err
	}
}

func (i *GCEInstance) Stop(ctx gocontext.Context) error {
	op, err := i.client.Instances.Delete(i.projectID, i.ic.Zone.Name, i.instance.Name).Do()
	if err != nil {
		return err
	}

	errChan := make(chan error)
	go func() {
		for {
			newOp, err := i.client.ZoneOperations.Get(i.projectID, i.ic.Zone.Name, op.Name).Do()
			if err != nil {
				errChan <- err
				return
			}

			if newOp.Status == "DONE" {
				if newOp.Error != nil {
					errChan <- &gceOpError{Err: newOp.Error}
					return
				}

				errChan <- nil
				return
			}
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *GCEInstance) ID() string {
	return fmt.Sprintf("%s:%s", i.instance.Name, i.imageName)
}
