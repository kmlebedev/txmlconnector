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

// ServerStatus reports a session state change, in the order the DLL emits the
// server_status callbacks: recover=true right on the link loss (the reconnect
// attempts start), then a plain Connected=true after a successful recovery, or
// Connected=false with Err when the session_timeout window expired and the
// client gave up (the XML client then issues connect again).
type ServerStatus struct {
	Connected bool
	Recover   bool
	Err       string
}

func (ServerStatus) isEvent() {}

func (*Trade) isEvent() {}
