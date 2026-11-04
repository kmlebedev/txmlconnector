package model

// Event is one event Client.Run delivers from an RQ_MAIN reply. The interface
// is closed: only this package implements it, so a new record family adds its
// event type here (or in a file of its own in this package).
type Event interface {
	isEvent()
}

// UnknownRecord is a record without a known layout: decoding of the RQ_MAIN
// payload stopped at Offset, and Payload holds the tail from the tag byte, so
// the caller can log or forward it. Polling continues.
type UnknownRecord struct {
	Tag     byte
	Offset  int
	Payload []byte
}

func (UnknownRecord) isEvent() {}

// ServerStatus reports a session state change. Connected is false once the link
// is lost; Recover is true when the session was restored through the reconnect
// path and the client is usable again. Err carries the failure text when the
// client could not continue.
type ServerStatus struct {
	Connected bool
	Recover   bool
	Err       string
}

func (ServerStatus) isEvent() {}

func (*Trade) isEvent() {}
