package ssh

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"io"
	"io/ioutil"

	"golang.org/x/crypto/ssh"

	"github.com/pkg/errors"
	"github.com/pkg/sftp"
)

type Dialer interface {
	Dial(address, username string) (Connection, error)
}
type Connection interface {
	UploadFile(path string, data []byte) (bool, error)
	RunCommand(command string, output io.Writer) (uint8, error)
	Close() error
}

func FormatPublicKey(key interface{}) ([]byte, error) {
	pubKey, err := ssh.NewPublicKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't use public key")
	}

	return ssh.MarshalAuthorizedKey(pubKey), nil
}

type SSHDialer struct {
	authMethods []ssh.AuthMethod
}

func NewDialerWithKey(key crypto.Signer) (*SSHDialer, error) {
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create signer from SSH key")
	}

	return &SSHDialer{
		authMethods: []ssh.AuthMethod{ssh.PublicKeys(signer)},
	}, nil
}

func NewDialerWithPassword(password string) (*SSHDialer, error) {
	return &SSHDialer{
		authMethods: []ssh.AuthMethod{ssh.Password(password)},
	}, nil
}

func NewDialer(keyPath, keyPassphrase string) (*SSHDialer, error) {
	file, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't read SSH key")
	}

	block, _ := pem.Decode(file)
	if block == nil {
		return nil, errors.Errorf("ssh key does not contain a valid PEM block")
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

func (d *SSHDialer) Dial(address, username string) (Connection, error) {
	client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{
		User: username,
		Auth: d.authMethods,
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

func (c *sshConnection) RunCommand(command string, output io.Writer) (uint8, error) {
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
		return uint8(err.ExitStatus()), nil
	default:
		return 0, errors.Wrap(err, "error running script")
	}
}

func (c *sshConnection) Close() error {
	return c.client.Close()
}
