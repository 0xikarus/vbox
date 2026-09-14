package workerprotocol

// Hello and Welcome are exchanged before multiplexing starts. They contain no
// credentials; credentials are carried only in the HTTPS Authorization header.
type Hello struct {
	Version      int      `json:"version"`
	Incarnation  string   `json:"incarnation"`
	Capabilities []string `json:"capabilities"`
	Binding      *Binding `json:"binding,omitempty"`
}
type Welcome struct {
	Capabilities []string `json:"capabilities,omitempty"`
	Version      int      `json:"version"`
	WorkerID     string   `json:"workerId"`
	AccountID    string   `json:"accountId"`
	SlotID       string   `json:"slotId"`
	Epoch        int64    `json:"epoch"`
}
