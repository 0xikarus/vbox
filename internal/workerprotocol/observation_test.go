package workerprotocol

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestObservationsKeepLatestWithoutBlockingExecution(t *testing.T) {
	controller, agent := pair(t)
	for i := uint64(1); i <= 20; i++ {
		if err := agent.PublishObservation(Observation{Sequence: i, Binding: request("unused").Binding}); err != nil {
			t.Fatal(err)
		}
	}
	// An ordinary stream proves the socket reader progressed beyond snapshots,
	// even though the controller has not consumed its observation mailbox.
	stream, err := agent.Open(context.Background(), request("after-observations"))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	received, err := controller.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer received.Close()
	select {
	case latest := <-controller.Observations():
		if latest.Sequence != 20 {
			t.Fatalf("latest sequence=%d", latest.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("missing observation")
	}
	select {
	case <-controller.Observations():
		t.Fatal("retained obsolete snapshots")
	default:
	}
}
func TestObservationsRejectReplayWrongDirectionAndOversize(t *testing.T) {
	controller, agent := pair(t)
	valid := Observation{Sequence: 1, Binding: request("unused").Binding}
	if err := controller.PublishObservation(valid); err == nil {
		t.Fatal("controller published agent evidence")
	}
	large := valid
	large.Runtime = append(append([]byte{'"'}, bytes.Repeat([]byte("a"), 33*1024)...), '"')
	if err := agent.PublishObservation(large); err == nil {
		t.Fatal("oversize observation accepted")
	}
	if err := agent.PublishObservation(valid); err != nil {
		t.Fatal(err)
	}
	<-controller.Observations()
	if err := agent.PublishObservation(valid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.Done():
	case <-time.After(time.Second):
		t.Fatal("replayed sequence accepted")
	}
}
