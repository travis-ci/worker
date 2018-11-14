package ssh

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"io"
	"io/ioutil"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/pkg/errors"
	"github.com/pkg/sftp"
)

type Dialer interface {
	Dial(address, username string, timeout time.Duration) (Connection, error)
}
type Connection interface {
	UploadFile(path string, data []byte) (bool, error)
	DownloadFile(path string) ([]byte, error)
	RunCommand(command string, output io.Writer) (int32, error)
	Close() error
}

func FormatPublicKey(key interface{}) ([]byte, error) {
	pubKey, err := ssh.NewPublicKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't use public key")
	}

	return ssh.MarshalAuthorizedKey(pubKey), nil
}

type AuthDialer struct {
	authMethods []ssh.AuthMethod
}

func NewDialerWithKey(key crypto.Signer) (*AuthDialer, error) {
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create signer from SSH key")
	}

	return &AuthDialer{
		authMethods: []ssh.AuthMethod{ssh.PublicKeys(signer)},
	}, nil
}

func NewDialerWithPassword(password string) (*AuthDialer, error) {
	return &AuthDialer{
		authMethods: []ssh.AuthMethod{ssh.Password(password)},
	}, nil
}

func NewDialerWithKeyWithoutPassPhrase(pemBytes []byte) (*AuthDialer, error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create signer from SSH key")
	}
	return &AuthDialer{
		authMethods: []ssh.AuthMethod{ssh.PublicKeys(signer)},
	}, nil
}

func NewDialer(keyPath, keyPassphrase string) (*AuthDialer, error) {
	file, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't read SSH key")
	}

	block, _ := pem.Decode(file)
	if block == nil {
		return nil, errors.Errorf("ssh key does not contain a valid PEM block")
	}

	if keyPassphrase == "" {
		return NewDialerWithKeyWithoutPassPhrase(file)
	}

	der, err := x509.DecryptPEMBlock(block, []byte(keyPassphrase))
	if err != nil {
		return nil, errors.Wrap(err, "couldn't decrypt SSH key")
	}

	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't parse SSH key")
	}

	return NewDialerWithKey(key)
}

func (d *AuthDialer) Dial(address, username string, timeout time.Duration) (Connection, error) {
	client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{
		User:    username,
		Auth:    d.authMethods,
		Timeout: timeout,
		// TODO: Verify server public key against something (optionally)?
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		return nil, errors.Wrap(err, "couldn't connect to SSH server")
	}

	return &sshConnection{client: client}, nil
}

type sshConnection struct {
	client *ssh.Client
}

func (c *sshConnection) UploadFile(path string, data []byte) (bool, error) {
	sftp, err := sftp.NewClient(c.client)
	if err != nil {
		return false, errors.Wrap(err, "couldn't create SFTP client")
	}
	defer sftp.Close()

	_, err = sftp.Lstat(path)
	if err == nil {
		return true, errors.New("file already existed")
	}

	f, err := sftp.Create(path)
	if err != nil {
		return false, errors.Wrap(err, "couldn't create file")
	}

	_, err = f.Write(data)
	if err != nil {
		return false, errors.Wrap(err, "couldn't write contents to file")
	}

	return false, nil
}

func (c *sshConnection) DownloadFile(path string) ([]byte, error) {
	sftp, err := sftp.NewClient(c.client)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create SFTP client")
	}
	defer sftp.Close()

	// TODO: enforce file size limit

	_, err = sftp.Lstat(path)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't stat file")
	}

	f, err := sftp.OpenFile(path, os.O_RDONLY)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't open file")
	}

	buf, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't read contents of file")
	}

	return buf, nil
}

func (c *sshConnection) RunCommand(command string, output io.Writer) (int32, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return 0, errors.Wrap(err, "error creating SSH session")
	}
	defer session.Close()

	err = session.RequestPty("xterm", 40, 80, ssh.TerminalModes{})
	if err != nil {
		return 0, errors.Wrap(err, "error requesting PTY")
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Run(command)

	if err == nil {
		return 0, nil
	}

	switch err := err.(type) {
	case *ssh.ExitError:
		return int32(err.ExitStatus()), nil
	default:
		return 0, errors.Wrap(err, "error running script")
	}
}

func (c *sshConnection) Close() error {
	return c.client.Close()
}
