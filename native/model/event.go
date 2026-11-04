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

func (*Trade) isEvent() {}
