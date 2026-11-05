// Package seeddata embeds the offline market-data snapshot of the AI terminal:
// real TRANSAQ demo-server candles, quotations and order books captured by
// tools/terminal-seed. It contains no account, client or login data.
package seeddata

import "embed"

//go:embed data/*.json.gz
var FS embed.FS

// Dir is the directory of the embedded snapshots inside FS.
const Dir = "data"
