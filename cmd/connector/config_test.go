package main

import "testing"

func TestParseInitXML(t *testing.T) {
	cfg, msg := parseInitXML(`<init log_path="/tmp/logs" log_level="2" logfile_lifetime="3600"/>`)
	if msg != "" || cfg.LogPath != "/tmp/logs" || cfg.LogLevel != 2 || cfg.LogfileLifetime != 3600 {
		t.Fatalf("cfg=%+v msg=%q", cfg, msg)
	}
	cases := map[string]string{
		"":                                   "Не удалось инициализировать библиотеку. параметр функции равен нулю",
		"<init log_path>":                    "Не удалось инициализировать библиотеку. Error reading Attributes.",
		"<root/>":                            "Не удалось инициализировать библиотеку. не найден элемент &lt;init&gt;",
		"<init/>":                            "Не удалось инициализировать библиотеку. не найден обязательный атрибут &lt;log_path&gt;",
		`<init log_path="x"/>`:               "Не удалось инициализировать библиотеку. не найден обязательный атрибут &lt;log_level&gt;",
		`<init log_path="x" log_level="4"/>`: "Не удалось инициализировать библиотеку. Wrong log level.",
	}
	for input, want := range cases {
		if _, got := parseInitXML(input); got != want {
			t.Fatalf("parseInitXML(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestServiceInfoResponse(t *testing.T) {
	cases := []struct {
		request string
		body    string
		code    int
	}{
		{"<request/>", "Empty request", 3},
		{"<request><value></value></request>", "Empty value text", 3},
		{"<request><value>unknown</value></request>", `Unknown parameter "unknown"`, 3},
		{"<request><value>version</value></request>", "<service_info><version>6.47.2.26.4</version></service_info>", 0},
		{"<request><value>queue_size</value></request>", "<service_info><queue_size>0</queue_size></service_info>", 0},
		{"<request><value>version</value><value>queue_size</value></request>", "<service_info><version>6.47.2.26.4</version><queue_size>0</queue_size></service_info>", 0},
	}
	for _, tc := range cases {
		body, code := serviceInfoResponse(tc.request, "6.47.2.26.4")
		if body != tc.body || code != tc.code {
			t.Fatalf("serviceInfoResponse(%q) = %q,%d; want %q,%d", tc.request, body, code, tc.body, tc.code)
		}
	}
}

func TestValidateCommandXML(t *testing.T) {
	cases := map[string]string{
		"<x/>":            "Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function",
		"<x>":             "Error reading Element value.",
		"<command":        "Failed to read Element name",
		"<command id=\"x": "Error parsing Element.",
	}
	for in, want := range cases {
		if got := validateCommandXML(in); got != want {
			t.Fatalf("validateCommandXML(%q) = %q, want %q", in, got, want)
		}
	}
	if got := validateCommandXML("<command id=\"x\"/>"); got != "" {
		t.Fatalf("valid command rejected: %q", got)
	}
}
