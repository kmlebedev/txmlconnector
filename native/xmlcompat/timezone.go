package xmlcompat

// The server describes a time zone with a 16-byte block (RQ_TIME reply and
// the R_SEC_NEW tail). It looks like Windows TIME_ZONE_INFORMATION reduced to
// 16-bit fields: bias, standard bias, daylight bias, then transition dates.
// The DLL resolves the block to a Windows zone name (FUN_180433f30); the
// table holds the blocks seen on the Finam demo server, each checked against
// the DLL's server_tz and sec_tz (17 176 securities).
var timeZoneNames = map[[16]byte]string{
	{}:                                   "UTC",
	{0x4c, 0xff, 0x00, 0x00, 0xc4, 0xff}: "Russian Standard Time", // bias -180, daylight -60
}

// TimeZoneName returns the Windows time zone name the DLL reports for a
// server time-zone block, or false for a block not seen yet.
func TimeZoneName(block [16]byte) (string, bool) {
	name, ok := timeZoneNames[block]
	return name, ok
}

// DecodeCP1251 converts a server string to UTF-8.
func DecodeCP1251(s string) string { return cp1251(s) }
