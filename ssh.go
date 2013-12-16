package main

import (
	"bytes"
	"code.google.com/p/go.crypto/ssh"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"
)

// An SSHConnection manages an SSH connection to a server.
type SSHConnection struct {
	client *ssh.ClientConn
	logger *log.Logger
}

type singlePassword struct {
	password string
}

func (pw singlePassword) Password(user string) (string, error) {
	return pw.password, nil
}

type singleKeyring struct {
	signer ssh.Signer
}

func newSingleKeyring(path, passphrase string) (*singleKeyring, error) {
	privateKey, err := parseSSHKey(path, passphrase)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, err
	}

	return &singleKeyring{signer: signer}, nil
}

func (sk *singleKeyring) Key(i int) (ssh.PublicKey, error) {
	if i != 0 {
		return nil, nil
	}

	return sk.signer.PublicKey(), nil
}

func (sk *singleKeyring) Sign(i int, rand io.Reader, data []byte) ([]byte, error) {
	if i != 0 {
		return nil, fmt.Errorf("unknown key %d", i)
	}

	return sk.signer.Sign(rand, data)
}

func parseSSHKey(path, passphrase string) (*rsa.PrivateKey, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(file)

	der, err := x509.DecryptPEMBlock(block, []byte(passphrase))
	if err != nil {
		return nil, err
	}

	return x509.ParsePKCS1PrivateKey(der)
}

func clientAuthFromSSHInfo(info VMSSHInfo) (auths []ssh.ClientAuth, err error) {
	if info.SSHKeyPath != "" {
		keyring, err := newSingleKeyring(info.SSHKeyPath, info.SSHKeyPassphrase)
		if err != nil {
			return nil, err
		}

		auths = append(auths, ssh.ClientAuthKeyring(keyring))
	}
	if info.Password != "" {
		auths = append(auths, ssh.ClientAuthPassword(singlePassword{info.Password}))
	}

	return
}

// NewSSHConnection creates an SSH connection using the connection information
// for the given server.
func NewSSHConnection(server VM, logPrefix string) (*SSHConnection, error) {
	sshInfo := server.SSHInfo()

	conn, err := net.DialTimeout("tcp", sshInfo.Addr, 5*time.Second)
	if err != nil {
		return nil, err
	}

	auths, err := clientAuthFromSSHInfo(sshInfo)
	if err != nil {
		return nil, err
	}
	sshConfig := &ssh.ClientConfig{
		User: sshInfo.Username,
		Auth: auths,
	}
	client, err := ssh.Client(conn, sshConfig)
	logger := log.New(os.Stdout, fmt.Sprintf("%s-ssh: ", logPrefix), log.Ldate|log.Ltime)
	return &SSHConnection{client: client, logger: logger}, err
}

// Start starts the given command and returns as soon as the command has
// started. It does not wait for the command to finish. The returned channel
// will send the exit code and then close when the command is finished. If the
// exit code sent is -1 then there was an error running the build.
func (c *SSHConnection) Start(cmd string, output io.Writer) (<-chan int, error) {
	session, err := c.createSession()
	if err != nil {
		return nil, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Start(cmd)

	exitCodeChan := make(chan int, 1)
	go func() {
		defer session.Close()
		err := session.Wait()
		if err == nil {
			exitCodeChan <- 0
		} else {
			switch err := err.(type) {
			case *ssh.ExitError:
				if err.ExitStatus() != 0 {
					exitCodeChan <- err.ExitStatus()
				} else {
					c.logger.Printf("SSHConnection.Start: An error occurred while running the command: %v\n", err)
					exitCodeChan <- -1
				}
			default:
				c.logger.Printf("SSHConnection.Start: An I/O error occurred: %v\n", err)
				exitCodeChan <- -1
			}
		}
		close(exitCodeChan)
	}()

	return exitCodeChan, err
}

// Run runs a command and blocks until the command has finished. An error is
// returned if the command exited with a non-zero command.
func (c *SSHConnection) Run(cmd string) error {
	session, err := c.createSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

func (c *SSHConnection) createSession() (*ssh.Session, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, err
	}

	return session, session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
}

// UploadFile uploads the given content to the file on the remote server given
// by the path.
func (c *SSHConnection) UploadFile(path string, content []byte) error {
	session, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	go func() {
		io.Copy(stdin, bytes.NewReader(content))
		stdin.Close()
	}()

	return session.Run(fmt.Sprintf("cat > %s", path))
}

// Close closes the SSH connection.
func (c *SSHConnection) Close() {
	c.client.Close()
}
