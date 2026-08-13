package commands

import (
	"strings"
	"testing"
)

func TestEncodeRequestE(t *testing.T) {
	encoded, err := EncodeRequestE(Command{Id: "disconnect"})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the historical shape, including the empty alltrades element.
	if encoded != `<command id="disconnect"><alltrades></alltrades></command>` {
		t.Fatalf("encoded = %q", encoded)
	}
}

func TestEncodeRequestEReturnsMarshalError(t *testing.T) {
	_, err := EncodeRequestE(struct {
		Value chan int `xml:"value"`
	}{Value: make(chan int)})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("error = %v", err)
	}
}
