package records

// R_NEWS (0x35) and R_NEWS_UPLOAD (0x36) are the news records (plan task 24,
// D). Their body layouts are not recovered yet (I); no decoder is registered
// until RE delivers the field table.
const (
	TagNews       byte = 0x35
	TagNewsUpload byte = 0x36
)
