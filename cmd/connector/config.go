package main

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/kmlebedev/txmlconnector/cmd/connector/internal/dlllog"
)

// InitConfig is the <init log_path log_level use_minidump logfile_lifetime/> payload.
type InitConfig struct {
	LogPath         string
	LogLevel        int
	UseMinidump     bool
	LogfileLifetime int
}

const prefixInit = "Не удалось инициализировать библиотеку. "

// parseInitXML reproduces the DLL InitializeEx lifecycle checks and exact O
// texts (docs/protocol/cabi.md §4, cabi-reference.json initialize_ex_variants).
// A non-empty message is the <error> payload text without the wrapper.
func parseInitXML(text string) (InitConfig, string) {
	var cfg InitConfig
	text = strings.TrimSpace(text)
	if text == "" {
		return cfg, prefixInit + "параметр функции равен нулю"
	}
	dec := xml.NewDecoder(strings.NewReader(text))
	root, err := firstElement(dec)
	if err != nil {
		return cfg, prefixInit + "Error reading Attributes."
	}
	if root.Name.Local != "init" {
		return cfg, prefixInit + "не найден элемент &lt;init&gt;"
	}
	get := func(name string) (string, bool) {
		for _, attr := range root.Attr {
			if attr.Name.Local == name {
				return attr.Value, true
			}
		}
		return "", false
	}
	path, ok := get("log_path")
	if !ok || path == "" {
		return cfg, prefixInit + "не найден обязательный атрибут &lt;log_path&gt;"
	}
	levelText, ok := get("log_level")
	if !ok || levelText == "" {
		return cfg, prefixInit + "не найден обязательный атрибут &lt;log_level&gt;"
	}
	level, err := strconv.Atoi(levelText)
	if err != nil || level < 1 || level > 3 {
		return cfg, prefixInit + "Wrong log level."
	}
	cfg.LogPath, cfg.LogLevel = path, level
	// O: an absent logfile_lifetime keeps 7 days; a non-number counts as 0.
	cfg.LogfileLifetime = dlllog.DefaultLifetime
	if raw, ok := get("use_minidump"); ok {
		cfg.UseMinidump = strings.EqualFold(raw, "true")
	}
	if raw, ok := get("logfile_lifetime"); ok {
		cfg.LogfileLifetime, _ = strconv.Atoi(raw)
	}
	return cfg, ""
}

func firstElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start, nil
		}
	}
}

// serviceInfoResponse reproduces GetServiceInfo request parsing (cabi.md §9,
// cabi-reference.json service_info). code 0 success (body is service_info XML),
// code 3 request/value error (body is the <error> payload text).
func serviceInfoResponse(text, version string) (string, int) {
	text = strings.TrimSpace(text)
	dec := xml.NewDecoder(strings.NewReader(text))
	root, err := firstElement(dec)
	if err != nil || root.Name.Local != "request" {
		return "Empty request", 3
	}
	values := make([]string, 0, 1)
	for {
		tok, err := dec.Token()
		if err != nil {
			return "Empty request", 3
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "value" {
			var value string
			if err := dec.DecodeElement(&value, &start); err != nil {
				return "Empty value text", 3
			}
			if value == "" {
				return "Empty value text", 3
			}
			values = append(values, value)
		}
		if end, ok := tok.(xml.EndElement); ok && end.Name == root.Name {
			break
		}
	}
	if len(values) == 0 {
		return "Empty request", 3
	}
	var b strings.Builder
	b.WriteString("<service_info>")
	for _, value := range values {
		switch value {
		case "version":
			b.WriteString("<version>" + escapeXML(version) + "</version>")
		case "queue_size":
			b.WriteString("<queue_size>0</queue_size>")
		case "queue_mem_used":
			b.WriteString("<queue_mem_used>0</queue_mem_used>")
		default:
			return fmt.Sprintf("Unknown parameter %q", value), 3
		}
	}
	b.WriteString("</service_info>")
	return b.String(), 0
}

func escapeXML(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
