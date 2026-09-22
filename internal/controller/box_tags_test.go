package controller

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeBoxTags(t *testing.T) {
	tags, err := normalizeBoxTags([]string{" backend ", "Priority", "BACKEND"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tags, []string{"backend", "Priority"}) {
		t.Fatalf("tags=%v", tags)
	}
	if _, err := normalizeBoxTags([]string{""}); err == nil {
		t.Fatal("empty tag accepted")
	}
	if _, err := normalizeBoxTags([]string{strings.Repeat("x", 33)}); err == nil {
		t.Fatal("long tag accepted")
	}
	if _, err := normalizeBoxTags([]string{"bad\ntag"}); err == nil {
		t.Fatal("control character accepted")
	}
	values := make([]string, maxBoxTags+1)
	for i := range values {
		values[i] = "tag"
	}
	if _, err := normalizeBoxTags(values); err == nil {
		t.Fatal("too many tags accepted")
	}
}
