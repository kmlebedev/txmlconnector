package main

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// InitConfig is the <init log_path log_level logfile_lifetime/> payload of
// InitializeEx (docs/COMPATIBILITY.md §2). The log files themselves are not
// implemented yet (I/TODO RE-2 task-70); the fields are parsed and stored.
type InitConfig struct {
	LogPath         string `xml:"log_path,attr"`
	LogLevel        int    `xml:"log_level,attr"`
	LogfileLifetime int    `xml:"logfile_lifetime,attr"`
}

// parseInitXML parses InitializeEx XML. Exact DLL error texts are pending RE-2;
// the returned errors are explicit and stable for the Go tests.
func parseInitXML(text string) (InitConfig, error) {
	var cfg InitConfig
	text = strings.TrimSpace(text)
	if text == "" {
		return cfg, fmt.Errorf("Empty initialization XML (I)")
	}
	if err := xml.Unmarshal([]byte(text), &cfg); err != nil {
		return cfg, fmt.Errorf("invalid initialization XML: %w", err)
	}
	if cfg.LogLevel != 0 && (cfg.LogLevel < 1 || cfg.LogLevel > 3) {
		return cfg, fmt.Errorf("invalid log level %d (I)", cfg.LogLevel)
	}
	return cfg, nil
}

// ServiceInfoRequest is <request><value>version|queue_size|queue_mem_used</value></request>.
type ServiceInfoRequest struct {
	Value string `xml:"value"`
}

func parseServiceInfoRequest(text string) (ServiceInfoRequest, error) {
	var req ServiceInfoRequest
	text = strings.TrimSpace(text)
	if text == "" {
		return req, fmt.Errorf("Empty request")
	}
	if err := xml.Unmarshal([]byte(text), &req); err != nil {
		return req, fmt.Errorf("invalid service info request: %w", err)
	}
	if req.Value == "" {
		return req, fmt.Errorf("Empty value text")
	}
	return req, nil
}

// serviceInfoValue renders the placeholder <service_info> XML. The exact DLL
// response shape is pending RE-2 task-70; this function is covered by Go tests
// and marked I in docs/COMPATIBILITY.md.
func serviceInfoValue(value, version string) (string, bool) {
	switch value {
	case "version":
		return "<service_info><value>version</value><version>" + xmlEscape(version) + "</version></service_info>", true
	case "queue_size":
		return "<service_info><value>queue_size</value><queue_size>0</queue_size></service_info>", true
	case "queue_mem_used":
		return "<service_info><value>queue_mem_used</value><queue_mem_used>0</queue_mem_used></service_info>", true
	default:
		return "", false
	}
}
