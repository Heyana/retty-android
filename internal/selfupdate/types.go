package selfupdate

import "time"

// Requests never accept a download URL, executable path or shell command.
type Request struct {
	Op      string `json:"op,omitempty"`
	Action  string `json:"action"`
	Version string `json:"version,omitempty"`
}

type Status struct {
	Kind      string    `json:"kind,omitempty"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available,omitempty"`
	Phase     string    `json:"phase"`
	Error     string    `json:"error,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
}

func (s Status) Busy() bool {
	return s.Phase == "downloading" || s.Phase == "installing" || s.Phase == "restarting"
}
