package main

import (
	"time"
)

// A VMProvider talks to the API for a VM provider.
type VMProvider interface {
	// Start starts a server with the given hostname. It will block until the VM
	// is booted and ready to be SSHed into.
	Start(hostname string, bootTimeout time.Duration) (VM, error)
}

// A VM represents a single VM instance.
type VM interface {
	// SSHInfo returns the information necessary for connecting to the server.
	SSHInfo() VMSSHInfo
	// Destroy shuts down the VM (usually in a 'disconnect-the-power' sense)
	Destroy() error
}

// VMSSHInfo contains the necessary information for connecting to the
// server using SSH. Either a password or an SSH key and passphrase should be
// provided. In the case that both are provided, then the SSH key will be
// used for authentication.
type VMSSHInfo struct {
	Addr             string
	Username         string
	Password         string
	SSHKeyPath       string
	SSHKeyPassphrase string
}
