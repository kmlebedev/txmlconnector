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

func TestBoardsDecodesEveryBoard(t *testing.T) {
	var boards Boards
	raw := `<boards><board id="AUCT"><name>Аукцион</name><market>1</market><type>2</type></board>` +
		`<board id="TQBR"><name>Т+ Акции и ДР</name><market>1</market><type>1</type></board></boards>`
	if err := xml.Unmarshal([]byte(raw), &boards); err != nil {
		t.Fatal(err)
	}
	if len(boards.Items) != 2 || boards.Items[1].ID != "TQBR" || boards.Items[1].Type != 1 {
		t.Fatalf("boards = %+v", boards.Items)
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
