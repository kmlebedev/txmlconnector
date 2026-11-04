package model

import "time"

// Message is one <message> callback of R_MSG (tag 0x01, PDF 4.33). The plan
// Interface fixes these fields; the wire layout is not recovered yet (I).
type Message struct {
	Time   time.Time
	Urgent bool
	From   string
	Text   string
}

func (*Message) isEvent() {}
