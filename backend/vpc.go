package backend

import (
	"bytes"
	goctx "context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"io"
	mathrand "math/rand"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/ssh"
)

const (
	defaultVPCInstanceProfile  = "cx2-2x4"                                   // Cheapest instance available.
	defaultVPCImageID          = "r014-d87ce5f1-4977-497f-be8c-84e9f6fc7b0e" // ibm-ubuntu-20-04-2-minimal-amd64-1
	defaultVPCRegion           = "us-south"
	defaultVPCInstanceUsername = "travis"

	defaultVPCAPIRetries       = 120
	defaultVPCAPIRetryInterval = time.Second * 5
	defaultVPCSSHRetries       = 120
	defaultVPCSSHRetryInterval = time.Second * 2
)

var (
	vpcRegionAPIEndpoints = map[string]string{
		"us-south": "https://us-south.iaas.cloud.ibm.com/v1",
		"us-east":  "https://us-east.iaas.cloud.ibm.com/v1",
		"ca-tor":   "https://ca-tor.iaas.cloud.ibm.com/v1",
		"eu-gb":    "https://eu-gb.iaas.cloud.ibm.com/v1",
		"eu-de":    "https://eu-de.iaas.cloud.ibm.com/v1",
		"jp-tok":   "https://jp-tok.iaas.cloud.ibm.com/v1",
		"jp-osa":   "https://jp-osa.iaas.cloud.ibm.com/v1",
		"au-syd":   "https://au-syd.iaas.cloud.ibm.com/v1",
		"br-sao":   "https://br-sao.iaas.cloud.ibm.com/v1",
	}

	vpcEnvironmentVariables = map[string]string{
		"IC_API_KEY":         "[REQUIRED] API key with access to create VMs (required)",
		"REGION":             "region where everything goes",
		"INSTANCE_PROFILE":   "type of instance for each build",
		"RESOURCE_GROUP_ID":  "[REQUIRED] ID of the resource group to add VMs to",
		"VPC_ID":             "[REQUIRED] ID of the VPC instance to attach VMs to",
		"DEFAULT_IMAGE_ID":   "ID of the default image to boot VMs into",
		"SUBNET_IDS":         "[REQUIRED] list of subnet IDs to spawn VMs into",
		"SECURITY_GROUP_IDS": "list of security group IDs to attach to VMs",
		"USER_DATA":          "base64 encoded custom user data",
		"USERNAME":           "username to SSH into VM with",
		"API_RETRIES":        "number of times to retry API",
		"API_RETRY_INTERVAL": "how long to wait in between API retries",
		"SSH_RETRIES":        "number of times to retry SSH into instance",
		"SSH_RETRY_INTERVAL": "how long to wait in between API retries",
	}

	vpcStartupScript = template.Must(template.New("vpc-startup").Parse(`#!/usr/bin/env bash
cat > ~travis/.ssh/authorized_keys <<EOF
{{ .PublicKey }}
EOF
chown -R travis:travis ~travis/.ssh/

{{ .UserData }}
`))
)

func init() {
	Register("vpc", "IBM Cloud Virtual Servers for VPC", vpcEnvironmentVariables, newVPCProvider)
}

type vpcProvider struct {
	cfg              *config.ProviderConfig
	service          *vpcv1.VpcV1
	instanceProfile  string
	defaultImageID   string
	resourceGroupID  string
	vpcID            string
	subnetIDs        []string
	securityGroupIDs []string
	region           string
	userData         string
	username         string
	apiRetries       int
	apiRetryInterval time.Duration
	sshRetries       int
	sshRetryInterval time.Duration
}

type vpcInstance struct {
	provider  *vpcProvider
	instance  *vpcv1.Instance
	sshDialer ssh.Dialer
	sshKey    *vpcv1.Key
	hostname  string
	ip        string
}

func newVPCProvider(cfg *config.ProviderConfig) (Provider, error) {
	ret := &vpcProvider{
		cfg:              cfg,
		region:           defaultVPCRegion,
		instanceProfile:  defaultVPCInstanceProfile,
		defaultImageID:   defaultVPCImageID,
		username:         defaultVPCInstanceUsername,
		apiRetries:       defaultVPCAPIRetries,
		apiRetryInterval: defaultVPCAPIRetryInterval,
		sshRetries:       defaultVPCSSHRetries,
		sshRetryInterval: defaultVPCAPIRetryInterval,
	}
	if cfg.IsSet("REGION") {
		ret.region = cfg.Get("REGION")
	}
	if cfg.IsSet("INSTANCE_PROFILE") {
		ret.instanceProfile = cfg.Get("INSTANCE_PROFILE")
	}
	if cfg.IsSet("DEFAULT_IMAGE_ID") {
		ret.defaultImageID = cfg.Get("DEFAULT_IMAGE_ID")
	}
	if cfg.IsSet("RESOURCE_GROUP_ID") {
		ret.resourceGroupID = cfg.Get("RESOURCE_GROUP_ID")
	}
	if cfg.IsSet("VPC_ID") {
		ret.vpcID = cfg.Get("VPC_ID")
	}
	if cfg.IsSet("SUBNET_IDS") {
		ret.subnetIDs = strings.Split(cfg.Get("SUBNET_IDS"), ",")
	}
	if cfg.IsSet("SECURITY_GROUP_IDS") {
		ret.securityGroupIDs = strings.Split(cfg.Get("SECURITY_GROUP_IDS"), ",")
	}
	if cfg.IsSet("USER_DATA") {
		var userDataBytes []byte
		userDataBytes, err := base64.RawURLEncoding.DecodeString(cfg.Get("USER_DATA"))
		if err != nil {
			return nil, err
		}
		ret.userData = string(userDataBytes)
	}
	if cfg.IsSet("USERNAME") {
		ret.username = cfg.Get("USERNAME")
	}
	if cfg.IsSet("API_RETRIES") {
		c, err := strconv.ParseInt(cfg.Get("API_RETRIES"), 0, 32)
		if err != nil {
			return nil, err
		}
		ret.sshRetries = int(c)
	}
	if cfg.IsSet("API_RETRY_INTERVAL") {
		t, err := time.ParseDuration(cfg.Get("API_RETRY_INTERVAL"))
		if err != nil {
			return nil, err
		}
		ret.apiRetryInterval = t
	}
	if cfg.IsSet("SSH_RETRIES") {
		c, err := strconv.ParseInt(cfg.Get("SSH_RETRIES"), 0, 32)
		if err != nil {
			return nil, err
		}
		ret.sshRetries = int(c)
	}
	if cfg.IsSet("SSH_RETRY_INTERVAL") {
		t, err := time.ParseDuration(cfg.Get("SSH_RETRY_INTERVAL"))
		if err != nil {
			return nil, err
		}
		ret.sshRetryInterval = t
	}

	if _, ok := vpcRegionAPIEndpoints[ret.region]; !ok {
		return nil, fmt.Errorf("unknown region %s", ret.region)
	}

	vpcService, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{
		URL: vpcRegionAPIEndpoints[ret.region],
		Authenticator: &core.IamAuthenticator{
			ApiKey: cfg.Get("IC_API_KEY"),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create vpc service: %w", err)
	}
	vpcService.EnableRetries(ret.apiRetries, ret.apiRetryInterval)
	ret.service = vpcService

	return ret, nil
}

func (p *vpcProvider) Start(ctx goctx.Context, _ *StartAttributes) (i Instance, retErr error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/vpc")
	hostname := hostnameFromContext(ctx)

	key, sshDialer, err := p.createSSHKey(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			logger := logger.WithField("key", key.Name)
			logger.Info("cleaning up SSH key due to instance creation failure")
			if _, err := p.service.DeleteKeyWithContext(ctx, &vpcv1.DeleteKeyOptions{ID: key.ID}); err != nil {
				logger.WithError(err).Error("failed to cleanup SSH Key")
			}
			logger.Debug("cleaned up SSH key")
		}
	}()

	instance, err := p.createInstance(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			logger := logger.WithField("instance", instance.Name)
			logger.Info("cleaning up instance due to failure")
			if _, err := p.service.DeleteInstanceWithContext(ctx, &vpcv1.DeleteInstanceOptions{ID: instance.ID}); err != nil {
				logger.WithError(err).Error("failed to cleanup instance")
			}
			logger.Debug("cleaned up instance")

			// Hack: the next deferred call to delete the SSH key will fail if we attempt
			// to delete it too soon after deleting the instance. TODO: poll for instance
			// deletion before attempting to delete SSH key.
			<-time.After(time.Second * 5)
		}
	}()

	instance, err = p.waitForInstance(ctx, instance, sshDialer)
	if err != nil {
		return nil, err
	}

	return &vpcInstance{
		provider:  p,
		instance:  instance,
		sshDialer: sshDialer,
		sshKey:    key,
		hostname:  hostname,
	}, nil
}

func (p *vpcProvider) createSSHKey(ctx goctx.Context) (*vpcv1.Key, *ssh.AuthDialer, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/vpc")
	hostname := hostnameFromContext(ctx)

	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}
	publicKey, err := ssh.FormatPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	sshDialer, err := ssh.NewDialerWithKey(privateKey)
	if err != nil {
		return nil, nil, err
	}

	sshKeyOptions := &vpcv1.CreateKeyOptions{
		Name:          &hostname,
		ResourceGroup: &vpcv1.ResourceGroupIdentityByID{ID: &p.resourceGroupID},
	}
	sshKeyOptions.SetPublicKey(string(publicKey))
	logger.WithField("key", sshKeyOptions.Name).Debug("creating ssh key")
	key, _, err := p.service.CreateKeyWithContext(ctx, sshKeyOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to add ssh key to ibm cloud %w", err)
	}
	logger.WithField("key", key.Name).Debug("created ssh key")
	return key, sshDialer, nil
}

func (p *vpcProvider) createInstance(ctx goctx.Context, key *vpcv1.Key) (*vpcv1.Instance, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/vpc")
	instancePrototype, err := p.getInstancePrototype(ctx, key)
	if err != nil {
		return nil, err
	}
	logger.WithField("instance", instancePrototype.Name).Debug("creating vpc instance")
	instance, _, err := p.service.CreateInstanceWithContext(ctx, &vpcv1.CreateInstanceOptions{
		InstancePrototype: instancePrototype,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create vpc instane: %w", err)
	}
	logger.WithField("instance", instance.Name).Debug("created vpc instance")
	return instance, err
}

func (p *vpcProvider) getInstancePrototype(ctx goctx.Context, key *vpcv1.Key) (*vpcv1.InstancePrototypeInstanceByImage, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/vpc")
	hostname := hostnameFromContext(ctx)

	// Choose random subnet to balance VMs. Ideally multiple subnets are given that
	// are spread out across availability zones.
	subnetID := p.subnetIDs[mathrand.Int()%len(p.subnetIDs)]

	// Get the zone from the subnet, because this SDK requires we specify zone even
	// if it can be inferred by subnet.
	logger.WithField("id", subnetID).Debug("getting subnet details")
	subnet, _, err := p.service.GetSubnetWithContext(ctx, p.service.NewGetSubnetOptions(subnetID))
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet details: %w", err)
	}
	logger.WithField("subnet", subnet).Debug("got subnet details")

	// TODO: check if the availability zone is ready, and choose another subnet if not.

	userDataBuffer := bytes.Buffer{}
	if err := vpcStartupScript.Execute(&userDataBuffer, struct{ PublicKey, UserData string }{
		PublicKey: *key.PublicKey,
		UserData:  p.userData,
	}); err != nil {
		return nil, fmt.Errorf("failed to render user data: %w", err)
	}
	userData := userDataBuffer.String()

	instancePrototype := &vpcv1.InstancePrototypeInstanceByImage{
		Keys:          []vpcv1.KeyIdentityIntf{&vpcv1.KeyIdentityByID{ID: key.ID}},
		Profile:       &vpcv1.InstanceProfileIdentityByName{Name: &p.instanceProfile},
		Name:          &hostname,
		ResourceGroup: &vpcv1.ResourceGroupIdentityByID{ID: &p.resourceGroupID},
		UserData:      &userData,
		VPC:           &vpcv1.VPCIdentityByID{ID: &p.vpcID},
		Image:         &vpcv1.ImageIdentityByID{ID: &p.defaultImageID},
		PrimaryNetworkInterface: &vpcv1.NetworkInterfacePrototype{
			SecurityGroups: []vpcv1.SecurityGroupIdentityIntf{},
			Subnet:         &vpcv1.SubnetIdentityByID{ID: &subnetID},
		},
		Zone: &vpcv1.ZoneIdentityByName{Name: subnet.Zone.Name},
	}

	for _, secgroup := range p.securityGroupIDs {
		secgroup := secgroup
		instancePrototype.PrimaryNetworkInterface.SecurityGroups = append(
			instancePrototype.PrimaryNetworkInterface.SecurityGroups,
			&vpcv1.SecurityGroupIdentityByID{ID: &secgroup},
		)
	}
	return instancePrototype, nil
}

// waitForInstance blocks until the instance is fully ready. It also returns an
// updated instance struct with the latest information.
func (p *vpcProvider) waitForInstance(ctx goctx.Context, instance *vpcv1.Instance, sshDialer *ssh.AuthDialer) (*vpcv1.Instance, error) {
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "backend/vpc", "instance": instance.Name,
	})

	// Wait for the instance to go into the running state. We need to do this rather
	// than just waiting for SSH because we don't know the instance's IP address
	// until IBM Cloud considers it ready.
	var (
		ret *vpcv1.Instance
		err error
	)
	for i := 1; i <= p.apiRetries; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.apiRetryInterval):
			logger.Debugf("probing instance for readiness, attempt %d of %d", i, p.apiRetries)
			ret, _, err = p.service.GetInstanceWithContext(ctx, &vpcv1.GetInstanceOptions{ID: instance.ID})
			if err != nil || *ret.Status != "running" {
				logger.WithError(err).Debugf("readiness attempt failed, state: %s", *ret.Status)
				continue
			}

			logger.Info("instance is running")
			return ret, p.waitForInstanceSSH(ctx, instance, *ret.PrimaryNetworkInterface.PrimaryIpv4Address, sshDialer)
		}
	}
	return nil, errors.New("retry limit exceeded while waiting for instance to become ready")
}

func (p *vpcProvider) waitForInstanceSSH(ctx goctx.Context, instance *vpcv1.Instance, ip string, sshDialer *ssh.AuthDialer) error {
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "backend/vpc", "instance": instance.Name, "ip": ip, "username": p.username,
	})
	for i := 1; i <= p.sshRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.sshRetryInterval):
			logger.Debugf("probing instance for connectivity, attempt %d of %d", i, p.sshRetries)
			conn, err := sshDialer.Dial(fmt.Sprintf("%s:22", ip), p.username, time.Second)
			if err != nil {
				logger.WithError(err).Debugf("attempt failed")
				continue
			}

			if err := conn.Close(); err != nil {
				logger.WithError(err).Warn("failed to close SSH test connection")
			}
			logger.Info("instance is reachable")
			return nil
		}
	}
	return errors.New("retry limit exceeded while waiting to SSH into instance")
}

func (p *vpcProvider) StartWithProgress(ctx goctx.Context, startAttributes *StartAttributes, _ Progresser) (Instance, error) {
	return p.Start(ctx, startAttributes)
}

func (p *vpcProvider) Setup(ctx goctx.Context) error {
	// All setup is done in newVPCProvider()
	return nil
}

func (p *vpcProvider) SupportsProgress() bool {
	return false
}

func (i *vpcInstance) UploadScript(ctx goctx.Context, script []byte) error {
	// TODO
	return nil
}

func (i *vpcInstance) RunScript(ctx goctx.Context, writer io.Writer) (*RunResult, error) {
	// TODO
	return &RunResult{Completed: true}, nil
}

func (i *vpcInstance) DownloadTrace(ctx goctx.Context) ([]byte, error) {
	// TODO
	return nil, ErrDownloadTraceNotImplemented
}

func (i *vpcInstance) Stop(ctx goctx.Context) error {
	// TODO
	return nil
}

func (i *vpcInstance) StartupDuration() time.Duration {
	// TODO
	return 0
}

func (i *vpcInstance) ID() string {
	// TODO
	return ""
}

func (i *vpcInstance) ImageName() string {
	// TODO
	return ""
}

func (i *vpcInstance) Warmed() bool {
	return false
}

func (i *vpcInstance) SupportsProgress() bool {
	return false
}
