package model

import "time"

// Message is one <message> item of R_MSG (0x01, PDF 4.33, messages.md D/O).
// Time is the UTC instant (server wall date + the RQ_TIME bias); WallTime is the
// raw server wall clock, which the DLL prints in <date> as HH:MM:SS (the local
// machine conversion is I). The XML uses WallTime, not Time.
type Message struct {
	Time     time.Time
	WallTime time.Time
	Urgent   bool
	From     string
	Text     string
	Flags    byte // raw flags: bits 0x01/0x08 = urgent (O); other bits I
}

func (*Message) isEvent() {}

// Messages is the batch of all R_MSG records of ONE RQ_MAIN reply: the DLL
// emits one <messages> callback per response (O).
type Messages struct {
	Items []Message
}

func (*Messages) isEvent() {}
