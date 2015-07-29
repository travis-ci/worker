package backend

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/pborman/uuid"

	"github.com/henrikhodne/goblueboxapi"
	"github.com/pkg/sftp"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	"golang.org/x/crypto/ssh"
	gocontext "golang.org/x/net/context"
)

var (
	templateRegexp = regexp.MustCompile(`travis-([\w-]+)-\d{4}-\d{2}-\d{2}-\d{2}-\d{2}`)
	errNoBlueBoxIP = fmt.Errorf("no IP address assigned")
	blueBoxHelp    = map[string]string{
		"CUSTOMER_ID":             "[REQUIRED] account customer id",
		"API_KEY":                 "[REQUIRED] API key",
		"LOCATION_ID":             "[REQUIRED] location where job blocks will be provisioned",
		"PRODUCT_ID":              "[REQUIRED]",
		"IPV6_ONLY":               "boot all blocks with only an IPv6 address (default false)",
		"LANGUAGE_MAP_{LANGUAGE}": "Map the key specified in the key to the image associated with a different language",
	}
)

func init() {
	config.SetProviderHelp("BlueBox", blueBoxHelp)
}

type blueBoxProvider struct {
	client *goblueboxapi.Client
	cfg    *config.ProviderConfig
}

func newBlueBoxProvider(cfg *config.ProviderConfig) (*blueBoxProvider, error) {
	return &blueBoxProvider{
		client: goblueboxapi.NewClient(cfg.Get("CUSTOMER_ID"), cfg.Get("API_KEY")),
		cfg:    cfg,
	}, nil
}

func (b *blueBoxProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	password := generatePassword()
	params := goblueboxapi.BlockParams{
		Product:  b.cfg.Get("PRODUCT_ID"),
		Template: b.templateIDForLanguageGroup(startAttributes.Language, startAttributes.Group),
		Location: b.cfg.Get("LOCATION_ID"),
		Hostname: fmt.Sprintf("testing-bb-%s", uuid.NewUUID()),
		Username: "travis",
		Password: password,
		IPv6Only: b.cfg.Get("IPV6_ONLY") == "true",
	}

	startBooting := time.Now()

	block, err := b.client.Blocks.Create(params)
	if err != nil {
		return nil, err
	}

	blockReady := make(chan bool)
	go func(id string) {
		for {
			block, err = b.client.Blocks.Get(id)
			if err == nil && block.Status == "running" {
				blockReady <- true
				return
			}

			time.Sleep(5 * time.Second)
		}
	}(block.ID)

	select {
	case <-blockReady:
		metrics.TimeSince("worker.vm.provider.bluebox.boot", startBooting)
		return &blueBoxInstance{
			client:   b.client,
			block:    block,
			password: password,
		}, nil
	case <-ctx.Done():
		if block != nil {
			err := b.client.Blocks.Destroy(block.ID)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("block", block).WithField("err", err).Error("could not destroy block")
			}
		}

		if ctx.Err() == gocontext.DeadlineExceeded {
			metrics.Mark("worker.vm.provider.bluebox.boot.timeout")
		}
		return nil, ctx.Err()
	}
}

func (b *blueBoxProvider) templateIDForLanguageGroup(language, group string) string {
	languageMapSetting := fmt.Sprintf("LANGUAGE_MAP_%s", strings.ToUpper(language))
	if b.cfg.IsSet(languageMapSetting) {
		language = b.cfg.Get(languageMapSetting)
	}

	templates := b.latestTemplates()

	if templateID, ok := templates[fmt.Sprintf("%s-%s", language, group)]; group != "" && ok {
		return templateID
	}

	if templateID, ok := templates[language]; ok {
		return templateID
	}

	if t, ok := templates["default"]; ok {
		return t
	}

	return ""
}

func (b *blueBoxProvider) latestTemplates() map[string]string {
	latest := map[string]goblueboxapi.Template{}
	latestIDs := map[string]string{}

	templates, err := b.client.Templates.List()
	if err != nil {
		fmt.Printf("error trying to get templates: %s\n", err)
		return nil
	}

	for _, t := range templates {
		if t.Public || !strings.HasPrefix(t.Description, "travis-") {
			continue
		}

		language := templateRegexp.FindStringSubmatch(t.Description)[1]
		if _, ok := latest[language]; !ok || t.Created.After(latest[language].Created) {
			latest[language] = t
			latestIDs[language] = t.ID
		}
	}

	if _, ok := latestIDs["default"]; !ok {
		for templateName, id := range latestIDs {
			if templateName == "ruby" {
				latestIDs["default"] = id
			}
		}
	}

	return latestIDs
}

type blueBoxInstance struct {
	client   *goblueboxapi.Client
	block    *goblueboxapi.Block
	password string
}

func (i *blueBoxInstance) sshClient(ctx gocontext.Context) (*ssh.Client, error) {
	if len(i.block.IPs) == 0 {
		return nil, errNoBlueBoxIP
	}

	client, err := ssh.Dial("tcp6", fmt.Sprintf("[%s]:22", i.block.IPs[0].Address), &ssh.ClientConfig{
		User: "travis",
		Auth: []ssh.AuthMethod{
			ssh.Password(i.password),
		},
	})

	if err != nil {
		metrics.Mark("worker.vm.provider.bluebox.ssh.error")
		context.LoggerFromContext(ctx).WithField("block", i.block).WithField("vsh_id", i.block.VSHID).WithField("err", err).Error("error connecting to SSH")
	}

	return client, err
}

func (i *blueBoxInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	client, err := i.sshClient(ctx)
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

	_, err = f.Write(script)
	if err != nil {
		return err
	}

	return err
}

func (i *blueBoxInstance) RunScript(ctx gocontext.Context, output io.Writer) (*RunResult, error) {
	client, err := i.sshClient(ctx)
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

	err = session.Run("bash --login ~/build.sh")
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

func (i *blueBoxInstance) Stop(ctx gocontext.Context) error {
	return i.client.Blocks.Destroy(i.block.ID)
}

func (i *blueBoxInstance) ID() string {
	return i.block.ID
}
