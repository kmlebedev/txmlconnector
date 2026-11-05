package feed

import (
	"strings"
	"time"

	// Embed the IANA time-zone database: the live feed converts the exchange
	// wall clock to a UTC instant with time.LoadLocation, and this must work on
	// hosts without a system zoneinfo (minimal containers, Windows). The
	// package is stdlib-only, so the embedded copy is the safe default.
	_ "time/tzdata"
)

// windowsTimeZones maps Windows time-zone display names to IANA names. The
// TRANSAQ server reports Windows names in RQ_STARTUP and native ServerTZ returns
// them ("Russian Standard Time" on the demo), while the terminal contract (§6)
// exposes IANA names and time.LoadLocation understands only IANA/UTC. Without
// this map a Windows name would leave the exchange wall clock mislabelled as
// UTC, shifting ServerTime by the zone offset.
var windowsTimeZones = map[string]string{
	"Russian Standard Time":          "Europe/Moscow",
	"Moscow Standard Time":           "Europe/Moscow",
	"Kaliningrad Standard Time":      "Europe/Kaliningrad",
	"Samara Standard Time":           "Europe/Samara",
	"Yekaterinburg Standard Time":    "Asia/Yekaterinburg",
	"Omsk Standard Time":             "Asia/Omsk",
	"Novosibirsk Standard Time":      "Asia/Novosibirsk",
	"N. Central Asia Standard Time":  "Asia/Novosibirsk",
	"Krasnoyarsk Standard Time":      "Asia/Krasnoyarsk",
	"Irkutsk Standard Time":          "Asia/Irkutsk",
	"Yakutsk Standard Time":          "Asia/Yakutsk",
	"Vladivostok Standard Time":      "Asia/Vladivostok",
	"Magadan Standard Time":          "Asia/Magadan",
	"Kamchatka Standard Time":        "Asia/Kamchatka",
	"W. Europe Standard Time":        "Europe/Berlin",
	"Central Europe Standard Time":   "Europe/Budapest",
	"Central European Standard Time": "Europe/Warsaw",
	"GMT Standard Time":              "Europe/London",
	"FLE Standard Time":              "Europe/Kiev",
	"Turkey Standard Time":           "Europe/Istanbul",
	"E. Europe Standard Time":        "Europe/Chisinau",
	"UTC":                            "UTC",
	"Coordinated Universal Time":     "UTC",
}

// ianaTimeZone converts an exchange zone name to an IANA name: an IANA/UTC name
// is returned as is, a Windows name is mapped, an unknown name yields "".
func ianaTimeZone(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if _, err := time.LoadLocation(name); err == nil {
		return name
	}
	if iana, ok := windowsTimeZones[name]; ok {
		return iana
	}
	// Windows names are case-insensitive in practice; be lenient about it.
	lower := strings.ToLower(name)
	for windows, iana := range windowsTimeZones {
		if strings.ToLower(windows) == lower {
			return iana
		}
	}
	return ""
}

// exchangeZoneName is ianaTimeZone with a last-resort pass-through: an unknown
// name is kept as reported rather than silently dropped.
func exchangeZoneName(name string) string {
	if iana := ianaTimeZone(name); iana != "" {
		return iana
	}
	return strings.TrimSpace(name)
}

// serverTimeFromWall converts a server wall-clock time (a fake-UTC time built
// from the server's local calendar fields) to the real UTC instant in the given
// IANA exchange zone. ok is false when the zone is empty or unknown, so the
// caller can fall back instead of mislabelling the wall clock as UTC.
func serverTimeFromWall(serverWall time.Time, zone string) (time.Time, bool) {
	if zone == "" {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(
		serverWall.Year(), serverWall.Month(), serverWall.Day(),
		serverWall.Hour(), serverWall.Minute(), serverWall.Second(), 0, loc,
	).UTC(), true
}
