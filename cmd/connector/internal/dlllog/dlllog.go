// Package dlllog writes the log files of txmlconnector64-6.47.2.26.4.dll in
// its own format: <log_path>/YYYYMMDD_dsp.log, _xdf.log and _ts.log, CP1251,
// CRLF, appended across sessions. The formats and the level rules are the O/D
// facts of docs/protocol/logs.md.
package dlllog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/charmap"
)

const (
	// SystemVersion is the version line of _dsp.log and the [V] record of
	// _xdf.log (O).
	SystemVersion = "System version 6.47. TXmlConnector version 2.26.4"
	// TradeStreamVersion closes the "Start logging" header of _ts.log (O).
	TradeStreamVersion = "Transaq TXmlConnector, ver.6.47-T78-37-2.26.4"
	// DefaultLifetime is the logfile_lifetime the DLL applies when the
	// attribute is absent and with Initialize (O: 7 days kept, 8 removed).
	DefaultLifetime = 7
	// NoPrune disables the clean-up (O: logfile_lifetime="-1").
	NoPrune = -1
)

// ErrPathNotExist is the DLL refusal of a log_path that is not an existing
// directory (O: "Не удалось инициализировать библиотеку. Указанный путь не
// существует" for Initialize and InitializeEx).
var ErrPathNotExist = errors.New("Указанный путь не существует")

// Options configures a Logger.
type Options struct {
	Dir      string // log_path as the caller passed it (printed verbatim)
	Level    int    // 1..3
	Lifetime int    // logfile_lifetime in days; NoPrune keeps every file
	// PID, Now and ThreadID default to the process values; tests fix them.
	PID      int
	Now      func() time.Time
	ThreadID func() uint64
}

// Logger owns the three log files of one Initialize..UnInitialize cycle.
type Logger struct {
	mu               sync.Mutex
	dir              string
	level            int
	pid              int
	now              func() time.Time
	threadID         func() uint64
	date             string
	dspFile, xdfFile *os.File
	ts               *os.File
}

// Open checks log_path, removes expired files and starts a log session with
// the DLL's first records ("Open log", "System version", "Initializing...").
func Open(opt Options) (*Logger, error) {
	info, err := os.Stat(opt.Dir)
	if err != nil || !info.IsDir() {
		return nil, ErrPathNotExist
	}
	l := &Logger{dir: opt.Dir, level: opt.Level, pid: opt.PID, now: opt.Now, threadID: opt.ThreadID}
	if l.pid == 0 {
		l.pid = os.Getpid()
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.threadID == nil {
		l.threadID = func() uint64 { return 0 }
	}
	now := l.now()
	Prune(opt.Dir, opt.Lifetime, now)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.rotateLocked(now); err != nil {
		l.closeLocked()
		return nil, err
	}
	thread := l.thread("")
	l.dspLocked(now, thread, "info", fmt.Sprintf("Open log %s, level %d", opt.Dir, opt.Level))
	l.dspLocked(l.now(), thread, "info", SystemVersion)
	l.dspLocked(l.now(), thread, "info", "Initializing...")
	l.xdfLocked(l.now(), thread, "cmd", 'V', SystemVersion)
	return l, nil
}

// rotateLocked opens the files of now's date. The DLL names the files by date
// (O); reopening at a date change is I (boost-style daily file names).
func (l *Logger) rotateLocked(now time.Time) error {
	date := now.Format("20060102")
	if date == l.date && l.dspFile != nil {
		return nil
	}
	dsp, err := openAppend(filepath.Join(l.dir, date+"_dsp.log"))
	if err != nil {
		return err
	}
	xdf, err := openAppend(filepath.Join(l.dir, date+"_xdf.log"))
	if err != nil {
		dsp.Close()
		return err
	}
	l.closeLocked()
	l.dspFile, l.xdfFile, l.date = dsp, xdf, date
	return nil
}

func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// thread is the thread column: a name ("main", "clbk") or the caller's OS
// thread id in %04x (O).
func (l *Logger) thread(name string) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%04x", l.threadID())
}

func stamp(t time.Time) string {
	return fmt.Sprintf("%02d%02d%02d.%06d", t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1000)
}

func (l *Logger) writeLocked(f *os.File, line string) {
	if f == nil {
		return
	}
	_, _ = io.WriteString(f, encode(line)+"\r\n")
}

func (l *Logger) dspLocked(now time.Time, thread, kind, text string) {
	l.writeLocked(l.dspFile, fmt.Sprintf("%s [%04x] [%s] <%s> %s", stamp(now), l.pid, thread, kind, text))
}

func (l *Logger) xdfLocked(now time.Time, thread, kind string, dir byte, text string) {
	l.writeLocked(l.xdfFile, fmt.Sprintf("%s [%04x] [%s] <%s> [%c] %s", stamp(now), l.pid, thread, kind, dir, text))
}

// Info, Warning and Error write a _dsp.log record; every _dsp.log record is
// written at all levels (O: level 1 and 3 sessions log the same lines).
// thread is a fixed name ("main") or "" for the caller's thread.
func (l *Logger) Info(thread, text string)    { l.dsp(thread, "info", text) }
func (l *Logger) Warning(thread, text string) { l.dsp(thread, "warning", text) }
func (l *Logger) Error(thread, text string)   { l.dsp(thread, "error", text) }

func (l *Logger) dsp(thread, kind, text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.rotateLocked(now) != nil {
		return
	}
	l.dspLocked(now, l.thread(thread), kind, text)
}

// Command writes the [I] record of SendCommand: <cmd> for a <command>,
// <bad_cmd> for any other text (O). The password is masked.
func (l *Logger) Command(thread, text string, ok bool) {
	kind := "cmd"
	if !ok {
		kind = "bad_cmd"
	}
	l.xdf(thread, kind, 'I', MaskCommand(text))
}

// Result writes the [R] record: <res> for <result>, <error> for <error> (O).
func (l *Logger) Result(thread, text string, isError bool) {
	kind := "res"
	if isError {
		kind = "error"
	}
	l.xdf(thread, kind, 'R', text)
}

func (l *Logger) xdf(thread, kind string, dir byte, text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.rotateLocked(now) != nil {
		return
	}
	l.xdfLocked(now, l.thread(thread), kind, dir, text)
}

// Callback writes the [O] record after the client callback returned, with
// its run time in whole microseconds (O: a 40 ms callback logs [4xxxxu]),
// when the message level passes the current log level (D: FUN_1801191d0).
func (l *Logger) Callback(d time.Duration, xml string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if CallbackLevel(xml) > l.level {
		return
	}
	now := l.now()
	if l.rotateLocked(now) != nil {
		return
	}
	l.xdfLocked(now, "clbk", "info", 'O', "["+strconv.FormatInt(d.Microseconds(), 10)+"u] "+xml)
}

// level3Callbacks are the callbacks the DLL logs only at level 3. D:
// FUN_18017ffc0 gives message types 1..7 level 3 and every other type level
// 2; O (level-2 demo session, 17 types): these names were not logged, while
// server_status, client, orders and positions were. ticks share the RQ_MAIN
// market-data group of alltrades (I).
var level3Callbacks = map[string]bool{
	"markets": true, "boards": true, "candlekinds": true, "securities": true, "pits": true,
	"sec_info_upd": true, "sec_info": true, "overnight": true, "connector_version": true,
	"alltrades": true, "quotations": true, "quotes": true, "candles": true, "ticks": true,
}

// CallbackLevel is the log level from which a callback is written.
func CallbackLevel(xml string) int {
	if level3Callbacks[rootName(xml)] {
		return 3
	}
	return 2
}

func rootName(xml string) string {
	s := strings.TrimLeft(xml, " \t\r\n")
	if !strings.HasPrefix(s, "<") {
		return ""
	}
	s = s[1:]
	end := strings.IndexAny(s, " \t\r\n/>")
	if end < 0 {
		return s
	}
	return s[:end]
}

// SetLevel writes the SetLogLevel records of a valid level and applies it.
func (l *Logger) SetLevel(thread string, level int) {
	if l == nil {
		return
	}
	l.dsp(thread, "info", fmt.Sprintf("Changing log level. Requested level is %d", level))
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
	l.dsp(thread, "info", fmt.Sprintf("Log level changed successfully. New level is %d", level))
}

// SetLevelFailed writes the SetLogLevel records of a rejected level.
func (l *Logger) SetLevelFailed(thread string, level int, reason string) {
	if l == nil {
		return
	}
	l.dsp(thread, "info", fmt.Sprintf("Changing log level. Requested level is %d", level))
	l.dsp(thread, "error", "Log level change failed: "+reason)
}

// TSStart opens _ts.log (on the first connection) and writes the header of a
// connection attempt (O: two CRLF, then "Start logging dd/mm/yyyy HH:MM:SS").
func (l *Logger) TSStart() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.tsLocked(now) != nil {
		return
	}
	_, _ = io.WriteString(l.ts, "\r\n\r\nStart logging "+now.Format("02/01/2006 15:04:05")+" "+TradeStreamVersion+"\r\n")
}

// TS writes a trade-stream record "HH:MM:SS.mmm {tid} text" (O: tid in hex).
func (l *Logger) TS(text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.tsLocked(now) != nil {
		return
	}
	line := fmt.Sprintf("%s.%03d {%x} %s", now.Format("15:04:05"), now.Nanosecond()/1e6, l.threadID(), text)
	l.writeLocked(l.ts, line)
}

func (l *Logger) tsLocked(now time.Time) error {
	if err := l.rotateLocked(now); err != nil {
		return err
	}
	name := filepath.Join(l.dir, l.date+"_ts.log")
	if l.ts != nil && l.ts.Name() == name {
		return nil
	}
	f, err := openAppend(name)
	if err != nil {
		return err
	}
	if l.ts != nil {
		l.ts.Close()
	}
	l.ts = f
	return nil
}

// Finalize writes the UnInitialize records and closes the files (O:
// "Finalizing...", "Shutdown...", "Waiting for callback thread...",
// "Shutdown OK"). waitCallbacks runs between the last two records.
func (l *Logger) Finalize(waitCallbacks func()) {
	if l == nil {
		if waitCallbacks != nil {
			waitCallbacks()
		}
		return
	}
	l.Info("", "Finalizing...")
	l.Info("", "Shutdown...")
	l.Info("", "Waiting for callback thread...")
	if waitCallbacks != nil {
		waitCallbacks()
	}
	l.Info("", "Shutdown OK")
	l.Close()
}

// Close closes the files without records.
func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
	if l.ts != nil {
		l.ts.Close()
		l.ts = nil
	}
}

func (l *Logger) closeLocked() {
	if l.dspFile != nil {
		l.dspFile.Close()
	}
	if l.xdfFile != nil {
		l.xdfFile.Close()
	}
	l.dspFile, l.xdfFile = nil, nil
}

// Console writes a record the DLL prints to stdout while no log is open
// (O: "[YYYY-MM-DD HH:MM:SS.uuuuuu] [0x%08x] [info]    text", CP1251, every
// record at info).
func Console(w io.Writer, now time.Time, tid uint64, text string) {
	_, _ = io.WriteString(w, fmt.Sprintf("[%s.%06d] [0x%08x] [info]    %s%s",
		now.Format("2006-01-02 15:04:05"), now.Nanosecond()/1000, tid, encode(text), consoleEOL))
}

var logName = regexp.MustCompile(`^(\d{8})_(dsp|xdf|ts)\.log$`)

// Prune removes YYYYMMDD_{dsp,xdf,ts}.log files whose name date is more than
// lifetime days before now (O: lifetime 3 keeps 1..3 days, removes 4; 0 keeps
// only today; NoPrune or any negative value keeps all).
func Prune(dir string, lifetime int, now time.Time) {
	if lifetime < 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, e := range entries {
		m := logName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		day, err := time.Parse("20060102", m[1])
		if err != nil {
			continue
		}
		if int(today.Sub(day).Hours()/24) > lifetime {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

var (
	passwordElem = regexp.MustCompile(`(<(?:password|oldpass|newpass)>)[^<]*(</)`)
	passwordAttr = regexp.MustCompile(`(\b(?:password|oldpass|newpass)=")[^"]*(")`)
)

// MaskCommand replaces passwords with "*" as the DLL does for <password> (O);
// change_pass oldpass/newpass are masked the same way (I: never logged here).
func MaskCommand(text string) string {
	text = passwordElem.ReplaceAllString(text, "${1}*${2}")
	return passwordAttr.ReplaceAllString(text, "${1}*${2}")
}

// encode converts UTF-8 to CP1251 like the DLL; characters outside CP1251
// become '?'.
func encode(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	enc := charmap.Windows1251
	for _, r := range s {
		if c, ok := enc.EncodeRune(r); ok {
			b.WriteByte(c)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}
