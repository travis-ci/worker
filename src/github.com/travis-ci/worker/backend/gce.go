package backend

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"text/template"
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
	defaultGCEZone             = "us-central1-a"
	defaultGCEMachineType      = "n1-standard-2"
	defaultGCENetwork          = "default"
	defaultGCEDiskSize         = int64(20)
	defaultGCELanguage         = "minimal"
	defaultGCEBootPollSleep    = 3 * time.Second
	defaultGCEUploadRetries    = uint64(10)
	defaultGCEUploadRetrySleep = 5 * time.Second
	gceImagesFilter            = "name eq ^travis-ci-%s.+"
)

var (
	gceHelp = fmt.Sprintf(`
               PROJECT_ID - [REQUIRED] GCE project id
             ACCOUNT_JSON - [REQUIRED] account JSON config
             SSH_KEY_PATH - [REQUIRED] path to ssh key used to access job vms
         SSH_PUB_KEY_PATH - [REQUIRED] path to ssh public key used to access job vms
       SSH_KEY_PASSPHRASE - [REQUIRED] passphrase for ssh key given as ssh_key_path
                     ZONE - zone name (default %q)
             MACHINE_TYPE - machine name (default %q)
                  NETWORK - machine name (default %q)
                DISK_SIZE - disk size in GB (default %v)
  LANGUAGE_MAP_{LANGUAGE} - Map the key specified in the key to the image associated
                            with a different language
         DEFAULT_LANGUAGE - default language to use when looking up image (default %q)
          BOOT_POLL_SLEEP - sleep interval between polling server for instance status (default %v)
           UPLOAD_RETRIES - number of times to attempt to upload script before erroring (default %d)
       UPLOAD_RETRY_SLEEP - sleep interval between script upload attempts (default %v)

`, defaultGCEZone, defaultGCEMachineType, defaultGCENetwork,
		defaultGCEDiskSize, defaultGCELanguage, defaultGCEBootPollSleep, defaultGCEUploadRetries, defaultGCEUploadRetrySleep)

	errGCEMissingIPAddressError = fmt.Errorf("no IP address found")

	gceStartupScript = template.Must(template.New("gce-startup").Parse(`#!/usr/bin/env bash
cat > ~travis/.ssh/authorized_keys <<EOF
{{ .SSHPubKey }}
EOF
`))
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

type gceProvider struct {
	client    *compute.Service
	projectID string
	ic        *gceInstanceConfig
	cfg       *config.ProviderConfig

	bootPollSleep    time.Duration
	defaultLanguage  string
	uploadRetries    uint64
	uploadRetrySleep time.Duration
}

type gceInstanceConfig struct {
	MachineType  *compute.MachineType
	Zone         *compute.Zone
	Network      *compute.Network
	DiskType     string
	DiskSize     int64
	SSHKeySigner ssh.Signer
	SSHPubKey    string
}

type gceInstance struct {
	client   *compute.Service
	provider *gceProvider
	instance *compute.Instance
	ic       *gceInstanceConfig

	authUser string

	projectID string
	imageName string
}

func newGCEProvider(cfg *config.ProviderConfig) (*gceProvider, error) {
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

	if !cfg.IsSet("SSH_PUB_KEY_PATH") {
		return nil, fmt.Errorf("expected SSH_PUB_KEY_PATH config key")
	}

	sshKeyBytes, err := ioutil.ReadFile(sshKeyPath)

	if err != nil {
		return nil, err
	}

	sshPubKeyPath := cfg.Get("SSH_PUB_KEY_PATH")

	if !cfg.IsSet("SSH_KEY_PASSPHRASE") {
		return nil, fmt.Errorf("expected SSH_KEY_PASSPHRASE config key")
	}

	sshPubKeyBytes, err := ioutil.ReadFile(sshPubKeyPath)

	if err != nil {
		return nil, err
	}

	sshKeyPassphrase := cfg.Get("SSH_KEY_PASSPHRASE")

	block, _ := pem.Decode(sshKeyBytes)
	if block == nil {
		return nil, fmt.Errorf("ssh key does not contain a valid PEM block")
	}

	der, err := x509.DecryptPEMBlock(block, []byte(sshKeyPassphrase))
	if err != nil {
		return nil, err
	}

	parsedKey, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, err
	}

	sshKeySigner, err := ssh.NewSignerFromKey(parsedKey)
	if err != nil {
		return nil, err
	}

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

	bootPollSleep := defaultGCEBootPollSleep
	if cfg.IsSet("BOOT_POLL_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("BOOT_POLL_SLEEP"))
		if err != nil {
			return nil, err
		}
		bootPollSleep = si

	}
	uploadRetries := defaultGCEUploadRetries
	if cfg.IsSet("UPLOAD_RETRIES") {
		ur, err := strconv.ParseUint(cfg.Get("UPLOAD_RETRIES"), 10, 64)
		if err != nil {
			return nil, err
		}
		uploadRetries = ur
	}

	uploadRetrySleep := defaultGCEUploadRetrySleep
	if cfg.IsSet("UPLOAD_RETRY_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("UPLOAD_RETRY_SLEEP"))
		if err != nil {
			return nil, err
		}
		uploadRetrySleep = si
	}

	defaultLanguage := defaultGCELanguage
	if cfg.IsSet("DEFAULT_LANGUAGE") {
		defaultLanguage = cfg.Get("DEFAULT_LANGUAGE")
	}

	return &gceProvider{
		client:    client,
		projectID: projectID,
		cfg:       cfg,

		ic: &gceInstanceConfig{
			MachineType:  mt,
			Zone:         zone,
			Network:      nw,
			DiskType:     fmt.Sprintf("zones/%s/diskTypes/pd-standard", zone.Name),
			DiskSize:     diskSize,
			SSHKeySigner: sshKeySigner,
			SSHPubKey:    string(sshPubKeyBytes),
		},

		bootPollSleep:    bootPollSleep,
		defaultLanguage:  defaultLanguage,
		uploadRetries:    uploadRetries,
		uploadRetrySleep: uploadRetrySleep,
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

func (p *gceProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	logger := context.LoggerFromContext(ctx)

	var (
		image *compute.Image
		err   error
	)

	candidateLangs := []string{}

	mappedLang := fmt.Sprintf("LANGUAGE_MAP_%s", strings.ToUpper(startAttributes.Language))
	if p.cfg.IsSet(mappedLang) {
		logger.WithFields(logrus.Fields{
			"original": startAttributes.Language,
			"mapped":   p.cfg.Get(mappedLang),
		}).Debug("using mapped language to candidates")
		candidateLangs = append(candidateLangs, p.cfg.Get(mappedLang))
	} else {
		logger.WithFields(logrus.Fields{
			"original": startAttributes.Language,
		}).Debug("adding original language to candidates")
		candidateLangs = append(candidateLangs, startAttributes.Language)
	}
	candidateLangs = append(candidateLangs, p.defaultLanguage)

	for _, language := range candidateLangs {
		logger.WithFields(logrus.Fields{
			"original":  startAttributes.Language,
			"candidate": language,
		}).Debug("searching for image matching language")

		image, err = p.imageForLanguage(language)
		if err == nil {
			logger.WithFields(logrus.Fields{
				"candidate": language,
				"image":     image,
			}).Debug("found matching image for language")
			break
		}
	}

	if err != nil {
		return nil, err
	}

	scriptBuf := bytes.Buffer{}
	err = gceStartupScript.Execute(&scriptBuf, p.ic)
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
					Value: scriptBuf.String(),
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

			time.Sleep(p.bootPollSleep)
		}
	}()

	select {
	case inst := <-instanceReady:
		metrics.TimeSince("worker.vm.provider.gce.boot", startBooting)
		return &gceInstance{
			client:   p.client,
			provider: p,
			instance: inst,
			ic:       p.ic,

			authUser: "travis",

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

func (p *gceProvider) imageForLanguage(language string) (*compute.Image, error) {
	// TODO: add some TTL cache in here maybe?
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

func (i *gceInstance) sshClient() (*ssh.Client, error) {
	err := i.refreshInstance()
	if err != nil {
		return nil, err
	}

	ipAddr := i.getIP()
	if ipAddr == "" {
		return nil, errGCEMissingIPAddressError
	}

	return ssh.Dial("tcp", fmt.Sprintf("%s:22", ipAddr), &ssh.ClientConfig{
		User: i.authUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(i.ic.SSHKeySigner),
		},
	})
}

func (i *gceInstance) getIP() string {
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

func (i *gceInstance) refreshInstance() error {
	inst, err := i.client.Instances.Get(i.projectID, i.ic.Zone.Name, i.instance.Name).Do()
	if err != nil {
		return err
	}

	i.instance = inst
	return nil
}

func (i *gceInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	uploadedChan := make(chan error)

	go func() {
		var errCount uint64
		for {
			if ctx.Err() != nil {
				return
			}

			err := i.uploadScriptAttempt(ctx, script)
			if err == nil {
				uploadedChan <- nil
				return
			}

			errCount++
			if errCount > i.provider.uploadRetries {
				uploadedChan <- err
				return
			}

			time.Sleep(i.provider.uploadRetrySleep)
		}
	}()

	select {
	case err := <-uploadedChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *gceInstance) uploadScriptAttempt(ctx gocontext.Context, script []byte) error {
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

func (i *gceInstance) RunScript(ctx gocontext.Context, output io.WriteCloser) (*RunResult, error) {
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

func (i *gceInstance) Stop(ctx gocontext.Context) error {
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

			time.Sleep(i.provider.bootPollSleep)
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *gceInstance) ID() string {
	return fmt.Sprintf("%s:%s", i.instance.Name, i.imageName)
}
