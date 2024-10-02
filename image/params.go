package image

type Params struct {
	Infra    string
	Language string
	OsxImage string
	Dist     string
	Group    string
	OS       string

	JobID     uint64
	Repo      string
	GpuVMType string
}
