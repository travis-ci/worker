package backend

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	gocontext "context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/image"
	"github.com/travis-ci/worker/ssh"
)

var (
	defaultEC2ScriptLocation    = "/home/jonhenrik/travis-in-ec2"
	defaultEC2SSHDialTimeout    = 5 * time.Second
	defaultEC2ImageSelectorType = "env"
	defaultEC2SSHUserName       = "ubuntu"
	defaultEC2SSHPrivateKeyPath = "/home/jonhenrik/.ssh/devops-infra-del-sndbx.pem"
	defaultEC2ExecCmd           = "bash /home/ubuntu/build.sh"
	defaultEC2SubnetID          = ""
	defaultEC2InstanceType      = "t2.micro"
	defaultEC2Image             = "ami-02790d1ebf3b5181d"
	defaultEC2SecurityGroupIDs  = "default"
	defaultEC2EBSOptimized      = false
	defaultEC2DiskSize          = int64(8)
	defaultEC2UploadRetries     = uint64(120)
	defaultEC2UploadRetrySleep  = 1 * time.Second
)

func init() {
	Register("ec2", "EC2", map[string]string{
		"IMAGE_MAP":                "Map of which image to use for which language",
		"AWS_ACCESS_KEY_ID":        "AWS Access Key ID",
		"AWS_SECRET_ACCESS_KEY":    "AWS Secret Access Key",
		"REGION":                   "Which region to run workers in",  // Should be autodetected when running on EC2 instances?
		"INSTANCE_TYPE":            "Instance type to use for builds", // t2 and t3 are burstable
		"SUBNET_ID":                "Subnet ID to launch instances into",
		"EBS_OPTIMIZED":            "Whether or not to use EBS-optimized instances (Default: false)",
		"IAM_INSTANCE_PROFILE":     "This is not a good idea... for security, builds should provice API keys",
		"USER_DATA":                "Why?",
		"CPU_CREDIT_SPECIFICATION": "standard|unlimited (for faster boots)",
		"TAGS":               "Tags, how to deal with key value?",
		"DISK_SIZE":          fmt.Sprintf("Disk size in GB (default %d)", defaultEC2DiskSize),
		"SSH_DIAL_TIMEOUT":   fmt.Sprintf("connection timeout for ssh connections (default %v)", defaultEC2SSHDialTimeout),
		"UPLOAD_RETRIES":     fmt.Sprintf("number of times to attempt to upload script before erroring (default %d)", defaultEC2UploadRetries),
		"UPLOAD_RETRY_SLEEP": fmt.Sprintf("sleep interval between script upload attempts (default %v)", defaultEC2UploadRetrySleep),
		"SECURITY_GROUPS":    "Security groups to assign",
		"PUBLIC_IP":          "boot job instances with a public ip, disable this for NAT (default true)",
		"PUBLIC_IP_CONNECT":  "connect to the public ip of the instance instead of the internal, only takes effect if PUBLIC_IP is true (default true)",
	}, newEC2Provider)
}

type ec2Provider struct {
	cfg              *config.ProviderConfig
	execCmd          []string
	imageSelector    image.Selector
	awsSession       *session.Session
	instanceType     string
	defaultImage     string
	securityGroups   []string
	ebsOptimized     bool
	diskSize         int64
	uploadRetries    uint64
	uploadRetrySleep time.Duration
	sshDialTimeout   time.Duration
	publicIP         bool
	publicIPConnect  bool
	subnetID         string
}

func newEC2Provider(cfg *config.ProviderConfig) (Provider, error) {

	sshDialTimeout := defaultEC2SSHDialTimeout
	if cfg.IsSet("SSH_DIAL_TIMEOUT") {
		sd, err := time.ParseDuration(cfg.Get("SSH_DIAL_TIMEOUT"))
		if err != nil {
			return nil, err
		}
		sshDialTimeout = sd
	}

	execCmd := strings.Split(defaultEC2ExecCmd, " ")
	if cfg.IsSet("EXEC_CMD") {
		execCmd = strings.Split(cfg.Get("EXEC_CMD"), " ")
	}

	imageSelectorType := defaultEC2ImageSelectorType
	if cfg.IsSet("IMAGE_SELECTOR_TYPE") {
		imageSelectorType = cfg.Get("IMAGE_SELECTOR_TYPE")
	}

	if imageSelectorType != "env" && imageSelectorType != "api" {
		return nil, fmt.Errorf("invalid image selector type %q", imageSelectorType)
	}

	imageSelector, err := buildEC2ImageSelector(imageSelectorType, cfg)
	if err != nil {
		return nil, err
	}

	awsSession := &session.Session{}

	if cfg.IsSet("AWS_ACCESS_KEY_ID") && cfg.IsSet("AWS_SECRET_ACCESS_KEY") {
		config := aws.NewConfig().WithCredentialsChainVerboseErrors(true)
		staticCreds := credentials.NewStaticCredentials(cfg.Get("AWS_ACCESS_KEY_ID"), cfg.Get("AWS_SECRET_ACCESS_KEY"), "")
		if _, err = staticCreds.Get(); err != credentials.ErrStaticCredentialsEmpty {
			config.WithCredentials(staticCreds)
		}

		if err != nil {
			return nil, err
		}

		config = config.WithRegion("eu-west-1")
		config = config.WithMaxRetries(8)

		opts := session.Options{
			SharedConfigState: session.SharedConfigEnable,
			Config:            *config,
		}
		awsSession, err = session.NewSessionWithOptions(opts)

		if err != nil {
			return nil, err
		}
	}

	instanceType := defaultEC2InstanceType

	if cfg.IsSet("INSTANCE_TYPE") {
		instanceType = cfg.Get("INSTANCE_TYPE")
	}

	subnetID := ""
	if cfg.IsSet("SUBNET_ID") {
		subnetID = cfg.Get("SUBNET_ID")
	}

	defaultImage := defaultEC2Image
	if cfg.IsSet("DEFAULT_IMAGE") {
		defaultImage = cfg.Get("DEFAULT_IMAGE")
	}

	securityGroups := strings.Split(defaultEC2SecurityGroupIDs, ",")
	if cfg.IsSet("SECURITY_GROUP_IDS") {
		securityGroups = strings.Split(cfg.Get("SECURITY_GROUP_IDS"), ",")
	}

	ebsOptimized := defaultEC2EBSOptimized
	if cfg.IsSet("EBS_OPTIMIZED") {
		ebsOptimized = asBool(cfg.Get("EBS_OPTIMIZED"))
	}

	diskSize := defaultEC2DiskSize
	if cfg.IsSet("DISK_SIZE") {
		diskSize, err = strconv.ParseInt(cfg.Get("DISK_SIZE"), 10, 64)
		if err != nil {
			return nil, err
		}
	}

	uploadRetries := defaultEC2UploadRetries
	if cfg.IsSet("UPLOAD_RETRIES") {
		ur, err := strconv.ParseUint(cfg.Get("UPLOAD_RETRIES"), 10, 64)
		if err != nil {
			return nil, err
		}
		uploadRetries = ur
	}

	uploadRetrySleep := defaultEC2UploadRetrySleep
	if cfg.IsSet("UPLOAD_RETRY_SLEEP") {
		si, err := time.ParseDuration(cfg.Get("UPLOAD_RETRY_SLEEP"))
		if err != nil {
			return nil, err
		}
		uploadRetrySleep = si
	}

	publicIP := true
	if cfg.IsSet("PUBLIC_IP") {
		publicIP = asBool(cfg.Get("PUBLIC_IP"))
	}

	publicIPConnect := true
	if cfg.IsSet("PUBLIC_IP_CONNECT") {
		publicIPConnect = asBool(cfg.Get("PUBLIC_IP_CONNECT"))
	}

	return &ec2Provider{
		cfg:              cfg,
		sshDialTimeout:   sshDialTimeout,
		execCmd:          execCmd,
		imageSelector:    imageSelector,
		awsSession:       awsSession,
		instanceType:     instanceType,
		defaultImage:     defaultImage,
		securityGroups:   securityGroups,
		ebsOptimized:     ebsOptimized,
		diskSize:         diskSize,
		uploadRetries:    uploadRetries,
		uploadRetrySleep: uploadRetrySleep,
		publicIP:         publicIP,
		publicIPConnect:  publicIPConnect,
		subnetID:         subnetID,
	}, nil
}

func buildEC2ImageSelector(selectorType string, cfg *config.ProviderConfig) (image.Selector, error) {
	switch selectorType {
	case "env":
		return image.NewEnvSelector(cfg)
	case "api":
		baseURL, err := url.Parse(cfg.Get("IMAGE_SELECTOR_URL"))
		if err != nil {
			return nil, err
		}
		return image.NewAPISelector(baseURL), nil
	default:
		return nil, fmt.Errorf("invalid image selector type %q", selectorType)
	}
}

func (p *ec2Provider) StartWithProgress(ctx gocontext.Context, startAttributes *StartAttributes, progresser Progresser) (Instance, error) {
	return nil, nil
}

func (p *ec2Provider) SupportsProgress() bool {
	return false
}

func (p *ec2Provider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	startBooting := time.Now()
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/ec2_provider")
	hostName := hostnameFromContext(ctx)

	// Create EC2 service client
	svc := ec2.New(p.awsSession)

	keyResp, err := svc.CreateKeyPair(
		&ec2.CreateKeyPairInput{
			KeyName: aws.String(hostName),
		},
	)

	if err != nil {
		logger.WithField("err", err).Error("ooooehhh noesss!!!")
		return nil, err
	}

	privateKey := *keyResp.KeyMaterial
	sshDialer, err := ssh.NewDialerWithKeyWithoutPassPhrase([]byte(privateKey))

	if err != nil {
		return nil, err
	}

	imageID, err := p.imageSelector.Select(ctx, &image.Params{
		Language: startAttributes.Language,
		Infra:    "ec2",
	})

	if imageID == "default" {
		imageID = p.defaultImage
	}

	if err != nil {
		return nil, err
	}

	securityGroups := []*string{}
	for _, securityGroup := range p.securityGroups {
		securityGroups = append(securityGroups, &securityGroup)
	}

	blockDeviceMappings := []*ec2.BlockDeviceMapping{
		&ec2.BlockDeviceMapping{
			DeviceName: aws.String("/dev/sda1"),
			Ebs: &ec2.EbsBlockDevice{
				VolumeSize: aws.Int64(p.diskSize),
			},
		},
	}

	runOpts := &ec2.RunInstancesInput{
		ImageId:      aws.String(imageID),
		InstanceType: aws.String(p.instanceType),
		MaxCount:     aws.Int64(1),
		MinCount:     aws.Int64(1),
		KeyName:      keyResp.KeyName,

		CreditSpecification: &ec2.CreditSpecificationRequest{
			CpuCredits: aws.String("unlimited"), // TODO:
		},
		EbsOptimized:        aws.Bool(p.ebsOptimized),
		BlockDeviceMappings: blockDeviceMappings,
		TagSpecifications: []*ec2.TagSpecification{
			&ec2.TagSpecification{
				ResourceType: aws.String("instance"),
				Tags: []*ec2.Tag{
					&ec2.Tag{
						Key:   aws.String("Name"),
						Value: aws.String(hostName),
					},
				},
			},
		},
	}

	if p.subnetID != "" && p.publicIP {
		runOpts.NetworkInterfaces = []*ec2.InstanceNetworkInterfaceSpecification{
			{
				DeviceIndex:              aws.Int64(0),
				AssociatePublicIpAddress: &p.publicIP,
				SubnetId:                 aws.String(p.subnetID),
				Groups:                   securityGroups,
				DeleteOnTermination:      aws.Bool(true),
			},
		}
	} else {
		runOpts.SubnetId = aws.String(p.subnetID)
		runOpts.SecurityGroupIds = securityGroups
	}

	reservation, err := svc.RunInstances(runOpts)

	if err != nil {
		return nil, err
	}

	describeInstancesInput := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			reservation.Instances[0].InstanceId,
		},
	}

	instance := &ec2.Instance{}
	for {
		instances, err := svc.DescribeInstances(describeInstancesInput)

		if err != nil {
			return nil, err
		}
		instance = instances.Reservations[0].Instances[0]
		if instances != nil {
			address := *instance.PrivateDnsName
			if p.publicIPConnect {
				address = *instance.PublicDnsName
			}
			if address != "" {
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	return &ec2Instance{
		provider:     p,
		sshDialer:    sshDialer,
		endBooting:   time.Now(),
		startBooting: startBooting,
		instance:     instance,
	}, nil
}

func (p *ec2Provider) Setup(ctx gocontext.Context) error {
	return nil
}

type ec2Instance struct {
	provider     *ec2Provider
	startBooting time.Time
	endBooting   time.Time
	sshDialer    ssh.Dialer
	instance     *ec2.Instance
}

func (i *ec2Instance) UploadScript(ctx gocontext.Context, script []byte) error {
	defer context.TimeSince(ctx, "boot_poll_ssh", time.Now())
	uploadedChan := make(chan error)
	var lastErr error

	logger := context.LoggerFromContext(ctx).WithField("self", "backend/ec2_instance")

	// Wait for ssh to becom available
	go func() {
		var errCount uint64
		for {
			if ctx.Err() != nil {
				return
			}

			err := i.uploadScriptAttempt(ctx, script)
			if err != nil {
				logger.WithError(err).Debug("upload script attempt errored")
			} else {
				uploadedChan <- nil
				return
			}

			lastErr = err

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
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":  lastErr,
			"self": "backend/gce_instance",
		}).Info("stopping upload retries, error from last attempt")
		return ctx.Err()
	}

	//return i.uploadScriptAttempt(ctx, script)
}

func (i *ec2Instance) waitForSSH(port, timeout int) error {

	host := *i.instance.PrivateIpAddress
	if i.provider.publicIPConnect {
		host = *i.instance.PublicIpAddress
	}

	iter := 0
	for {
		_, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 1*time.Second)
		if err == nil {
			break
		}
		iter = iter + 1
		if iter > timeout {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (i *ec2Instance) uploadScriptAttempt(ctx gocontext.Context, script []byte) error {
	return i.uploadScriptSCP(ctx, script)
}

func (i *ec2Instance) uploadScriptSCP(ctx gocontext.Context, script []byte) error {
	conn, err := i.sshConnection(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	existed, err := conn.UploadFile("build.sh", script)
	if existed {
		return ErrStaleVM
	}
	if err != nil {
		return errors.Wrap(err, "couldn't upload build script")
	}
	return nil
}

func (i *ec2Instance) sshConnection(ctx gocontext.Context) (ssh.Connection, error) {
	ip := *i.instance.PrivateIpAddress
	if i.provider.publicIPConnect {
		ip = *i.instance.PublicIpAddress
	}
	return i.sshDialer.Dial(fmt.Sprintf("%s:22", ip), defaultEC2SSHUserName, i.provider.sshDialTimeout)
}

func (i *ec2Instance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	return i.runScriptSSH(ctx, output)
}

func (i *ec2Instance) runScriptSSH(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	conn, err := i.sshConnection(ctx)
	if err != nil {
		return &RunResult{Completed: false}, errors.Wrap(err, "couldn't connect to SSH server")
	}
	defer conn.Close()

	exitStatus, err := conn.RunCommand(strings.Join(i.provider.execCmd, " "), output)

	return &RunResult{Completed: err != nil, ExitCode: exitStatus}, errors.Wrap(err, "error running script")
}

func (i *ec2Instance) Stop(ctx gocontext.Context) error {
	logger := context.LoggerFromContext(ctx).WithField("self", "backend/ec2_provider")
	//hostName := hostnameFromContext(ctx)

	svc := ec2.New(i.provider.awsSession)

	instanceTerminationInput := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{
			i.instance.InstanceId,
		},
	}

	_, err := svc.TerminateInstances(instanceTerminationInput)

	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("Terminated instance %s with hostname %s", *i.instance.InstanceId, *i.instance.PublicDnsName))

	deleteKeyPairInput := &ec2.DeleteKeyPairInput{
		KeyName: i.instance.KeyName,
	}

	_, err = svc.DeleteKeyPair(deleteKeyPairInput)

	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("Deleted keypair %s", *i.instance.KeyName))

	return nil
}

func (i *ec2Instance) DownloadTrace(gocontext.Context) ([]byte, error) {
	return nil, nil
}

func (i *ec2Instance) SupportsProgress() bool {
	return false
}

func (i *ec2Instance) Warmed() bool {
	return false
}

func (i *ec2Instance) ID() string {
	if i.provider.publicIP {
		return *i.instance.PublicDnsName
	}
	return *i.instance.PrivateDnsName
}

func (i *ec2Instance) ImageName() string {
	return "ec2"
}

func (i *ec2Instance) StartupDuration() time.Duration {
	return i.endBooting.Sub(i.startBooting)
}
