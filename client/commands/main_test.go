package commands

import (
	"encoding/xml"
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

func TestServerStatusDecodesErrorMessage(t *testing.T) {
	var status ServerStatus
	raw := `<server_status connected="error">Неверный идентификатор, пароль или Touch Memory</server_status>`
	if err := xml.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatal(err)
	}
	if status.Connected != "error" {
		t.Fatalf("connected = %q", status.Connected)
	}
	if !strings.Contains(status.Message, "Неверный идентификатор") {
		t.Fatalf("message = %q", status.Message)
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
