package v1

import (
	"encoding/json"
	"testing"
)

func TestCreateLogicalBoxAutoStartDefaultsAndExplicitHibernate(t *testing.T) {
	for _, test := range []struct {
		body string
		want bool
	}{
		{`{"name":"new-box","provider":"railway"}`, true},
		{`{"name":"new-box","provider":"railway","allocateWhenReady":true}`, true},
		{`{"name":"new-box","provider":"railway","allocateWhenReady":false}`, false},
	} {
		var request CreateLogicalBoxRequest
		if err := json.Unmarshal([]byte(test.body), &request); err != nil {
			t.Fatal(err)
		}
		if got := request.ShouldAllocateWhenReady(); got != test.want {
			t.Fatalf("request %s auto-start=%v, want %v", test.body, got, test.want)
		}
	}
}
