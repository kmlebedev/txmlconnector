package model

import "time"

// NewsHeader is one <news_header> callback (R_NEWS 0x35 / R_NEWS_UPLOAD 0x36,
// PDF 4.19; news.md D/O). Time is the wire Unix timestamp; the XML render uses
// the local machine time like the DLL.
type NewsHeader struct {
	ID     int64
	Time   time.Time
	Source string
	Title  string
	Flag   byte // 0 in the O runs; meaning I
}

func (*NewsHeader) isEvent() {}

// NewsBody is one <news_body> callback (PDF 4.20, D form). The reply/emit
// mechanics of RQ_GETNEWS are not recovered yet (news.md I), so only the D
// shape is typed here.
type NewsBody struct {
	ID   int64
	Text string
}
