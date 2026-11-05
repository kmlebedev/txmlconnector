package session

// Portfolio request types (D, docs/protocol/connect.md, portfolios.md). The
// request and response layouts are not recovered yet (I): only the ids are
// pinned, no request is sent until RE completes the formats.
const (
	RqGetRpsRepo    byte = 82 // RQ_GET_RPSREPO
	RqMarginPos     byte = 84 // RQ_MARGIN_POS
	RqMarginPosList byte = 85 // RQ_MARGIN_POS_LIST
	RqFondPortfolio byte = 86 // RQ_FOND_PORTFOLIO
)
