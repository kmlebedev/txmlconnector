package main

import "testing"

func TestParseInitXML(t *testing.T) {
	cfg, err := parseInitXML(`<init log_path="/tmp/logs" log_level="2" logfile_lifetime="3600"/>`)
	if err != nil || cfg.LogPath != "/tmp/logs" || cfg.LogLevel != 2 || cfg.LogfileLifetime != 3600 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if _, err := parseInitXML(""); err == nil {
		t.Fatal("empty init XML accepted")
	}
	if _, err := parseInitXML(`<init log_level="9"/>`); err == nil {
		t.Fatal("invalid log level accepted")
	}
}

func TestParseServiceInfoRequest(t *testing.T) {
	req, err := parseServiceInfoRequest(`<request><value>version</value></request>`)
	if err != nil || req.Value != "version" {
		t.Fatalf("req=%+v err=%v", req, err)
	}
	if _, err := parseServiceInfoRequest(""); err == nil {
		t.Fatal("empty request accepted")
	}
	if _, err := parseServiceInfoRequest(`<request></request>`); err == nil {
		t.Fatal("empty value accepted")
	}
}

func TestServiceInfoValue(t *testing.T) {
	for _, value := range []string{"version", "queue_size", "queue_mem_used"} {
		info, ok := serviceInfoValue(value, "6.47.2.26.4")
		if !ok || info == "" {
			t.Fatalf("%s: %q %v", value, info, ok)
		}
	}
	if _, ok := serviceInfoValue("bogus", "v"); ok {
		t.Fatal("unknown value accepted")
	}
}
