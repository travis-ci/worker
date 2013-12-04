package main

// A VMProvider talks to the API for a VM provider.
type VMProvider interface {
	// Start starts a server with the given hostname.
	Start(hostname string) (VM, error)
}

// A VM represents a single VM instance.
type VM interface {
	// SSHInfo returns the information necessary for connecting to the server.
	SSHInfo() VMSSHInfo
	// Destroy shuts down the VM (usually in a 'disconnect-the-power' sense)
	Destroy() error
	// Refresh downloads the latest information about the VM from the API.
	Refresh() error
	// Ready returns true iff the server is done booting and is ready to be
	// SSHed into.
	Ready() bool
}

// VMSSHInfo contains the necessary information for connecting to the
// server using SSH.
type VMSSHInfo struct {
	Addr     string
	Username string
	Password string
}
