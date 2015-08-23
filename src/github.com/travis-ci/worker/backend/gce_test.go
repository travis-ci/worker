package backend

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/travis-ci/worker/config"
)

var (
	gceTestSSHKey = `
-----BEGIN RSA PRIVATE KEY-----
Proc-Type: 4,ENCRYPTED
DEK-Info: AES-128-CBC,08F3FE844168C0FAA56D70BB81F8E369

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
-----END RSA PRIVATE KEY-----
`
	gceTestSSHPubKey        = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQCxddTmYSYZoma31uM7PVWocPPqR0gXCXMo/0/EtTwaSz931fB897oqRCN6SPkyICjqiFiWun7Q3d8NUlbUhcNBsti2CKq944QMeKrg9OcvCAWHTq2BxJ9Z5d5V5YwKmoVlP5Zg9jTyv4eVWpQDtJCfpkpxbgjoiJop9KEnLJhu7YWLF/+VY5+35JHMem6yiQ32VB7x2X6y+ToWX3NZzCBcdEyCCGQIbpWUluVEALr7CloO6O3hMl7d6aiDxBIeozF1Q++rbpJ79JiJr29V9EpuWBQ3rwdbYoVzQS1jhWEGgN0OqPRl4nAXM84vmxuYGlqajb/ni9Ietu87Jj4nUyjN4eUTplXCCRzADt35xLN6IqI3GwXDIVOJIH9C0wipUKMMAzWMpHg1Y3WV6GMSROKRlP/qkDyLdUl3XCbVZp3fx28rKdJ4tZcThLkIav5wn3z1K+ZQLs2Kzqk1G5/C9iljHIO/ckfwh7mT54iqRv3qU1ACYXJd6UmD98i2SUMYUpcfIOaC1qZZdNHJy+EjpV6FPRSeMhnfq3kRTG7ezIJBMIcuPrWX6MXx/ZDcKl6MiDldEzzDXeG3xAjCr1gWoKuw7dBTH4Mqy4Dh1o2qPSpkv2v/ytk7W0t27J6ZYeK3d/WaXBF7JrCbiS6Mztw/kKUWSlOhF8Vl9UtvvTaoWjeSTw== contact+travis-worker@travis-ci.org"
	gceTestSSHKeyPassphrase = "ca21b20a836efea85aba98e1330959694ee31f7ab7f7dd6d2f723a1b8d31c9d2"
)

func gceTestSetup(t *testing.T, cfg *config.ProviderConfig) *gceProvider {
	var (
		td  string
		err error
	)

	if !cfg.IsSet("TEMP_DIR") {
		td, err = ioutil.TempDir("", "travis-worker")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Set("TEMP_DIR", td)
	}

	td = cfg.Get("TEMP_DIR")

	keyPath := filepath.Join(td, "test_rsa")
	pubKeyPath := filepath.Join(td, "test_rsa.pub")

	err = ioutil.WriteFile(keyPath, []byte(gceTestSSHKey), 0644)
	if err != nil {
		t.Fatal(err)
	}

	err = ioutil.WriteFile(pubKeyPath, []byte(gceTestSSHPubKey), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if !cfg.IsSet("SSH_KEY_PATH") {
		cfg.Set("SSH_KEY_PATH", keyPath)
	}

	if !cfg.IsSet("SSH_PUB_KEY_PATH") {
		cfg.Set("SSH_PUB_KEY_PATH", pubKeyPath)
	}

	if !cfg.IsSet("SSH_KEY_PASSPHRASE") {
		cfg.Set("SSH_KEY_PASSPHRASE", gceTestSSHKeyPassphrase)
	}

	p, err := newGCEProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return p.(*gceProvider)
}

func gceTestTeardown(p *gceProvider) {
	if p.cfg.IsSet("TEMP_DIR") {
		_ = os.RemoveAll(p.cfg.Get("TEMP_DIR"))
	}
}

func TestGCEStart(t *testing.T) {
	p := gceTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"ACCOUNT_JSON": "{}",
		"PROJECT_ID":   "project_id",
	}))

	defer gceTestTeardown(p)
}
