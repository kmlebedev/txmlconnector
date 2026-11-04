package model

import "time"

// NewsHeader is one <news_header> callback (R_NEWS 0x35, PDF 4.19). The plan
// Interface fixes these fields; the wire layout is not recovered yet (I).
type NewsHeader struct {
	ID     int64
	Time   time.Time
	Source string
	Title  string
}

func (*NewsHeader) isEvent() {}

// NewsBody is the get_news_body reply (PDF 4.20; layout I - only the identity
// is typed until RE delivers the field table).
type NewsBody struct {
	ID int64
}
