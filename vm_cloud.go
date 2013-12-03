package main

type VMCloudSSHInfo struct {
	Addr     string
	Username string
	Password string
}

type VMCloudServer interface {
	SSHInfo() VMCloudSSHInfo
	Destroy() error
	Refresh() error
	Ready() bool
}

type VMCloudAPI interface {
	Start(hostname string) (VMCloudServer, error)
}
