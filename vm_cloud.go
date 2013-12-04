package main

// A VMCloudAPI talks to the API for a VM provider.
type VMCloudAPI interface {
	// Start starts a server with the given hostname.
	Start(hostname string) (VMCloudServer, error)
}

// A VMCloudServer represents a single VM instance.
type VMCloudServer interface {
	// SSHInfo returns the information necessary for connecting to the server.
	SSHInfo() VMCloudSSHInfo
	// Destroy shuts down the VM (usually in a 'disconnect-the-power' sense)
	Destroy() error
	// Refresh downloads the latest information about the VM from the API.
	Refresh() error
	// Ready returns true iff the server is done booting and is ready to be
	// SSHed into.
	Ready() bool
}

// VMCloudSSHInfo contains the necessary information for connecting to the
// server using SSH.
type VMCloudSSHInfo struct {
	Addr     string
	Username string
	Password string
}
