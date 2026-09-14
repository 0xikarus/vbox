package workerprotocol

import (
	"encoding/json"
	"errors"
	"math"
)

// Observation contains runtime evidence only. It cannot establish provider
// readiness, storage ownership, or completion of a task.
type Observation struct {
	Sequence uint64          `json:"sequence"`
	Binding  Binding         `json:"binding"`
	Runtime  json.RawMessage `json:"runtime,omitempty"`
}

func (o Observation) valid() bool {
	b := o.Binding
	return o.Sequence > 0 && o.Sequence <= math.MaxInt64 && b.AccountID != "" && b.SlotID != "" && b.BoxID != "" && b.Assignment != "" && b.Incarnation != "" &&
		len(o.Runtime) <= 32*1024 && (len(o.Runtime) == 0 || json.Valid(o.Runtime))
}
func (p *Peer) PublishObservation(observation Observation) error {
	if p.odd || !observation.valid() {
		return errors.New("invalid worker observation")
	}
	return p.sendControl(frame{Kind: "observation", Observation: &observation})
}
func (p *Peer) Observations() <-chan Observation { return p.observations }
