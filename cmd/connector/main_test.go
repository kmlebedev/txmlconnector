//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kmlebedev/txmlconnector/cmd/connector/internal/dlllog"
)

const (
	benchDeliverMessage = "<quotes><security_code>SBER</security_code></quotes>"
	testLogDate         = "20261105"
)

func openTestLogger(tb testing.TB, dir string, level int) *dlllog.Logger {
	tb.Helper()
	log, err := dlllog.Open(dlllog.Options{
		Dir:      dir,
		Level:    level,
		Lifetime: dlllog.NoPrune,
		Now:      func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) },
		ThreadID: func() uint64 { return 1 },
	})
	if err != nil {
		tb.Fatalf("dlllog.Open: %v", err)
	}
	tb.Cleanup(log.Close)
	return log
}

func newBenchLogger(tb testing.TB, level int) *dlllog.Logger {
	tb.Helper()
	return openTestLogger(tb, tb.TempDir(), level)
}

func setBenchLogLevel(l *library, level int) {
	l.logLevel.Store(int32(level))
}

func TestDeliverCallbackLogGate(t *testing.T) {
	cases := []struct {
		name  string
		level int
		msg   string
		want  bool
	}{
		{"level1_server_status", 1, "<server_status/>", false},
		{"level1_quotes", 1, "<quotes/>", false},
		{"level2_server_status", 2, "<server_status/>", true},
		{"level2_unknown", 2, "<unknown/>", true},
		{"level2_quotes", 2, "<quotes/>", false},
		{"level2_ticks", 2, "<ticks/>", false},
		{"level3_quotes", 3, "<quotes/>", true},
		{"level3_ticks", 3, "<ticks/>", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := openTestLogger(t, dir, tc.level)
			l := &library{log: log}
			l.logLevel.Store(int32(tc.level))
			l.deliver(tc.msg) // no callback registered
			log.Close()

			data, err := os.ReadFile(filepath.Join(dir, testLogDate+"_xdf.log"))
			if err != nil {
				t.Fatalf("read _xdf.log: %v", err)
			}
			got := false
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "<info> [O]") && strings.Contains(line, tc.msg) {
					got = true
					break
				}
			}
			if got != tc.want {
				t.Fatalf("callback record for %q at level %d = %v, want %v; log:\n%s",
					tc.msg, tc.level, got, tc.want, data)
			}
		})
	}
}

func TestSetLogLevelUpdatesCallbackGate(t *testing.T) {
	dir := t.TempDir()
	log := openTestLogger(t, dir, 1)
	l := &library{log: log}
	l.logLevel.Store(1)
	apiMu.Lock()
	lib = l
	apiMu.Unlock()
	t.Cleanup(func() {
		apiMu.Lock()
		lib = nil
		apiMu.Unlock()
		log.Close()
	})

	if resp := SetLogLevel(3); resp != nil {
		msg := goString(resp)
		freeCString(resp)
		t.Fatalf("SetLogLevel(3) = %q, want nil", msg)
	}
	if got := l.logLevel.Load(); got != 3 {
		t.Fatalf("logLevel after SetLogLevel(3) = %d, want 3", got)
	}

	resp := SetLogLevel(4)
	if resp == nil {
		t.Fatal("SetLogLevel(4) returned nil, want an <error>")
	}
	msg := goString(resp)
	freeCString(resp)
	if !strings.Contains(msg, "Invalid log level") {
		t.Fatalf("SetLogLevel(4) = %q, want Invalid log level", msg)
	}
	if got := l.logLevel.Load(); got != 3 {
		t.Fatalf("logLevel after rejected SetLogLevel(4) = %d, want 3", got)
	}
}

func BenchmarkDeliverNoCallback(b *testing.B) {
	l := &library{}
	setBenchLogLevel(l, 1)
	b.ReportAllocs()
	for b.Loop() {
		l.deliver(benchDeliverMessage)
	}
}

func BenchmarkDeliverLevel2SkipsLevel3(b *testing.B) {
	l := &library{log: newBenchLogger(b, 2)}
	setBenchLogLevel(l, 2)
	b.ReportAllocs()
	for b.Loop() {
		l.deliver(benchDeliverMessage)
	}
}
