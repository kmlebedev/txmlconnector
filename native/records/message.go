package records

// R_MSG (0x01) carries the text <message> callback (plan task 25, D+PDF 4.33).
// The body layout is not recovered yet (I); no decoder is registered until RE.
const TagMessage byte = 0x01
