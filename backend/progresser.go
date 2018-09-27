package backend

type ProgressState int

const (
	ProgressNeutral ProgressState = iota
	ProgressSuccess
	ProgressFailure
)

func (ps ProgressState) String() string {
	switch ps {
	case ProgressSuccess:
		return "success"
	case ProgressFailure:
		return "failure"
	case ProgressNeutral:
		return "neutral"
	default:
		return "unknown"
	}
}

type ProgressEntry struct {
	Message    string        `json:"message"`
	State      ProgressState `json:"state"`
	Interrupts bool          `json:"interrupts"`
	Continues  bool          `json:"continues"`
	Raw        bool          `json:"raw"`
}

type Progresser interface {
	Progress(*ProgressEntry)
}

type NullProgresser struct{}

func (np *NullProgresser) Progress(_ *ProgressEntry) {}
