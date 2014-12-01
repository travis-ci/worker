package ssh

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"

	"code.google.com/p/go.crypto/ssh"
)

// AuthenticationMethod represents a method for authenticating as a user to an
// SSH server.
type AuthenticationMethod ssh.AuthMethod

// PasswordAuthentication creates an authentication method using the provided
// password.
func PasswordAuthentication(password string) AuthenticationMethod {
	return AuthenticationMethod(ssh.Password(password))
}

// SSHKeyAuthentication creates an authentication method using the SSH key at
// the given path, optionally encrypted with the given passphrase.
//
// An error is returned if there was an error reading or decrypting the SSH
// key.
func SSHKeyAuthentication(path, passphrase string) (AuthenticationMethod, error) {
	privateKey, err := parseSSHKey(path, passphrase)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, err
	}

	return AuthenticationMethod(ssh.PublicKeys(signer)), nil
}

func toSSHClientAuths(ams []AuthenticationMethod) (sshAms []ssh.AuthMethod) {
	for _, am := range ams {
		sshAms = append(sshAms, ssh.AuthMethod(am))
	}

	return
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

func (sk *singleKeyring) Key(i int) (ssh.PublicKey, error) {
	if i != 0 {
		return nil, nil
	}

	return sk.signer.PublicKey(), nil
}

func (sk *singleKeyring) Sign(i int, rand io.Reader, data []byte) (*ssh.Signature, error) {
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
