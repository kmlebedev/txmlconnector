//go:build cgo

// Package main builds the drop-in C ABI library over native.Connector. The
// lifecycle texts and codes follow the O reference (docs/protocol/cabi.md,
// cmd/connector/testdata/cabi-reference.json); the log files follow
// docs/protocol/logs.md.
package main

/*
#include <stdbool.h>
#include <stdlib.h>
#include <stdint.h>

typedef unsigned char BYTE;
typedef bool (*txc_callback)(BYTE*);
typedef bool (*txc_callback_ex)(BYTE*, void*);

static bool txc_invoke(txc_callback f, BYTE* p) { return f(p); }
static bool txc_invoke_ex(txc_callback_ex f, BYTE* p, void* u) { return f(p, u); }

#ifdef _WIN32
#include <windows.h>
static uint64_t txc_tid(void) { return (uint64_t)GetCurrentThreadId(); }
#elif defined(__APPLE__)
#include <pthread.h>
static uint64_t txc_tid(void) { uint64_t t = 0; pthread_threadid_np(NULL, &t); return t; }
#else
#include <unistd.h>
#include <sys/syscall.h>
static uint64_t txc_tid(void) { return (uint64_t)syscall(SYS_gettid); }
#endif
*/
import "C"

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/kmlebedev/txmlconnector/cmd/connector/internal/dlllog"
	"github.com/kmlebedev/txmlconnector/native"
)

var defaultVersion = "6.47.2.26.4"

func versionString() string {
	if v := os.Getenv("TXC_VERSION"); v != "" {
		return v
	}
	return defaultVersion
}

const (
	errInitPrefix  = "Не удалось инициализировать библиотеку. "
	errAlready     = "Библиотека уже инициализирована"
	errAlreadyInit = errInitPrefix + errAlready
	errNotInit     = "Библиотека не инициализирована"
	errNotInitCmd  = "Библиотека не инициализована. Необходимо выполнить функцию Initialize()"
	errNoLogPath   = "Не указан путь к директории для хранения лог-файлов"
)

func errString(text string) *C.uchar { return cString("<error>" + text + "</error>") }

type library struct {
	conn *native.Connector
	log  *dlllog.Logger
	// logLevel mirrors Logger.level for the hot-path [O] gate.
	logLevel atomic.Int32
}

var (
	apiMu sync.Mutex
	lib   *library

	cbMu sync.Mutex
	cb   C.txc_callback
	cbEx C.txc_callback_ex
	user unsafe.Pointer

	serviceMu       sync.Mutex
	lastServiceCall time.Time
)

// serviceInfoRateLimit is 5000 ms in the DLL; variable for fast Go tests.
var serviceInfoRateLimit = 5000 * time.Millisecond

func threadID() uint64 { return uint64(C.txc_tid()) }

func cString(s string) *C.uchar { return (*C.uchar)(unsafe.Pointer(C.CString(s))) }

func freeCString(p *C.uchar) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

func goString(p *C.uchar) string { return C.GoString((*C.char)(unsafe.Pointer(p))) }

// console prints a record the DLL writes to stdout while no log is open.
func console(text string) { dlllog.Console(os.Stdout, time.Now(), threadID(), text) }

// currentLibrary is the initialized library or nil before Initialize.
func currentLibrary() *library {
	apiMu.Lock()
	defer apiMu.Unlock()
	return lib
}

// record writes a _dsp.log record, or the stdout record without a log.
func record(log *dlllog.Logger, kind, text string) {
	if log == nil {
		console(text)
		return
	}
	switch kind {
	case "error":
		log.Error("", text)
	default:
		log.Info("", text)
	}
}

// initFailed logs a refused Initialize/InitializeEx (O: "Failed to initialize
// library[ [context]]: reason"; the context is printed for a second
// initialization) and returns the <error> text.
func initFailedLocked(context, reason string) *C.uchar {
	text := "Failed to initialize library"
	var log *dlllog.Logger
	if lib != nil {
		log = lib.log
		if context != "" {
			text += " [" + context + "]"
		}
	}
	record(log, "error", text+": "+reason)
	return errString(errInitPrefix + reason)
}

func startLocked(logPath string, level, lifetime int) *C.uchar {
	log, err := dlllog.Open(dlllog.Options{Dir: logPath, Level: level, Lifetime: lifetime, ThreadID: threadID})
	if err != nil {
		return initFailedLocked("", err.Error())
	}
	l := &library{log: log}
	l.logLevel.Store(int32(level)) // read by deliver before the connector starts
	streams := &tradeStreams{log: log}
	l.conn = native.New(native.Config{
		Version: versionString(),
		Dial:    streams.dial,
		Trace:   func(ev native.TraceEvent) { traceToLog(log, streams, ev) },
	})
	// The Connector delivers messages FIFO from one goroutine; the client
	// callback runs right there, so emitSync (disconnect's connected="false")
	// returns only after the client saw the message, as with the DLL (O:
	// _xdf.log shows the [O] record before disconnect's [R]).
	if err := l.conn.Start(l.deliver); err != nil {
		log.Close()
		return errString(err.Error())
	}
	lib = l
	return nil
}

// deliver runs on the Connector's delivery goroutine, the callback thread:
// FIFO, pinned to one OS thread; the [O] record is written after the client
// callback returned (D: FUN_1801191d0).
func (l *library) deliver(message string) {
	runtime.LockOSThread() // idempotent; the goroutine keeps its thread
	cbMu.Lock()
	f, fx, u := cb, cbEx, user
	cbMu.Unlock()
	// The DLL hands the client a C copy it frees with FreeMemory. With no
	// callback registered nobody can free it, so skip the C allocation
	// (benchstat -count=6: 92.6n -> 44.2n ns/op, p=0.002).
	// [O] is level-gated (docs/protocol/logs.md §3): skip the clock reads and
	// the logger lock when Callback would drop the record. Callback re-checks
	// the level under its own lock (benchstat -count=6: level-2 quotes
	// 71.5n -> 28.6n ns/op, p=0.002; level 1 44.2n -> 5.2n).
	level := int(l.logLevel.Load())
	logCallback := level >= 3 || (level >= 2 && dlllog.CallbackLevel(message) <= level)
	var start time.Time
	if logCallback {
		start = time.Now()
	}
	switch {
	case fx != nil:
		C.txc_invoke_ex(fx, cString(message), u) // return value does not stop delivery
	case f != nil:
		C.txc_invoke(f, cString(message))
	}
	if logCallback {
		l.log.Callback(time.Since(start), message)
	}
}

//export Initialize
func Initialize(logPath *C.uchar, logLevel C.int) *C.uchar {
	apiMu.Lock()
	defer apiMu.Unlock()
	if lib != nil {
		path := ""
		if logPath != nil {
			path = goString(logPath)
		}
		return initFailedLocked(fmt.Sprintf("Path: %s, Level: %d", path, int(logLevel)), errAlready)
	}
	if logPath == nil {
		return initFailedLocked("", errNoLogPath)
	}
	if logLevel < 1 || logLevel > 3 {
		return initFailedLocked("", "Wrong log level.")
	}
	return startLocked(goString(logPath), int(logLevel), dlllog.DefaultLifetime)
}

//export InitializeEx
func InitializeEx(configXML *C.uchar) *C.uchar {
	apiMu.Lock()
	defer apiMu.Unlock()
	// A NULL/empty argument is refused before the state check (O: _dsp.log of
	// an initialized library logs "Failed to initialize library: параметр
	// функции равен нулю").
	text := ""
	if configXML != nil {
		text = goString(configXML)
	}
	if strings.TrimSpace(text) == "" {
		return initFailedLocked("", "параметр функции равен нулю")
	}
	if lib != nil {
		return initFailedLocked(text, errAlready)
	}
	cfg, msg := parseInitXML(text)
	if msg != "" {
		return initFailedLocked("", strings.TrimPrefix(msg, errInitPrefix))
	}
	return startLocked(cfg.LogPath, cfg.LogLevel, cfg.LogfileLifetime)
}

//export UnInitialize
func UnInitialize() *C.uchar {
	apiMu.Lock()
	l := lib
	if l == nil {
		apiMu.Unlock()
		console("Finalizing...")
		return errString(errNotInit)
	}
	lib = nil
	apiMu.Unlock()
	// Close disconnects and returns once every queued callback ran.
	l.log.Finalize(func() { _ = l.conn.Close() })
	return nil
}

//export SetCallback
func SetCallback(f C.txc_callback) C.bool {
	cbMu.Lock()
	cb, cbEx, user = f, nil, nil
	cbMu.Unlock()
	return true
}

//export SetCallbackEx
func SetCallbackEx(f C.txc_callback_ex, userData unsafe.Pointer) C.bool {
	cbMu.Lock()
	cb, cbEx, user = nil, f, userData
	cbMu.Unlock()
	return true
}

//export SendCommand
func SendCommand(commandXML *C.uchar) *C.uchar {
	apiMu.Lock()
	l := lib
	apiMu.Unlock()
	var log *dlllog.Logger
	if l != nil {
		log = l.log
	}
	text := ""
	if commandXML != nil {
		text = goString(commandXML)
	}
	// The [I] record precedes the execution (O: callbacks emitted while the
	// command runs, e.g. disconnect's connected="false", follow it).
	wrapperErr := commandTextError(text)
	if log == nil {
		console(dlllog.MaskCommand(text))
	} else {
		log.Command("", text, wrapperErr == "")
	}
	result := wrapperErr
	if result == "" {
		result = sendCommand(l, text)
	}
	if log == nil {
		console(result)
	} else {
		log.Result("", result, strings.HasPrefix(result, "<error>"))
	}
	return cString(result)
}

// commandTextError is the wrapper-level <error> for text that is not a
// well-formed <command> ("" when it is); such text is logged as <bad_cmd>.
func commandTextError(text string) string {
	if strings.TrimSpace(text) == "" {
		return "<error>Error document empty.</error>"
	}
	if msg := validateCommandXML(text); msg != "" {
		return "<error>" + msg + "</error>"
	}
	return ""
}

func sendCommand(l *library, text string) string {
	if l == nil {
		return "<error>" + errNotInitCmd + "</error>"
	}
	result, err := l.conn.SendCommand(context.Background(), text)
	if err != nil {
		return "<error>" + err.Error() + "</error>"
	}
	return result
}

// validateCommandXML reproduces the DLL wrapper-level errors for non-<command>
// and malformed XML (cabi.md §7).
func validateCommandXML(text string) string {
	trimmed := strings.TrimSpace(text)
	dec := xml.NewDecoder(strings.NewReader(trimmed))
	var root string
	for {
		tok, err := dec.Token()
		if err != nil {
			if strings.HasPrefix(trimmed, "<command") && !strings.Contains(trimmed, ">") {
				if strings.Contains(trimmed, "=") {
					return "Error parsing Element."
				}
				return "Failed to read Element name"
			}
			return "Error reading Element value."
		}
		if start, ok := tok.(xml.StartElement); ok && root == "" {
			root = start.Name.Local
		}
		if end, ok := tok.(xml.EndElement); ok && root != "" && end.Name.Local == root {
			break
		}
	}
	if root != "command" {
		return "Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function"
	}
	return ""
}

//export FreeMemory
func FreeMemory(p *C.uchar) C.bool {
	freeCString(p)
	return true
}

//export SetLogLevel
func SetLogLevel(level C.int) *C.uchar {
	l := currentLibrary()
	var log *dlllog.Logger
	if l != nil {
		log = l.log
	}
	if level < 1 || level > 3 {
		const reason = "Invalid log level. Should be in range [1,3]"
		if log == nil {
			console(fmt.Sprintf("Changing log level. Requested level is %d", int(level)))
			console("Log level change failed: " + reason)
		} else {
			log.SetLevelFailed("", int(level), reason)
		}
		return errString(reason)
	}
	if log == nil {
		console(fmt.Sprintf("Changing log level. Requested level is %d", int(level)))
		console(fmt.Sprintf("Log level changed successfully. New level is %d", int(level)))
	} else {
		log.SetLevel("", int(level))
		l.logLevel.Store(int32(level))
	}
	return nil
}

//export GetServiceInfo
func GetServiceInfo(request *C.uchar, response **C.uchar) C.int {
	// The DLL checks the 5000 ms rate limit first, before the parameter checks
	// (docs/protocol/cabi.md §9; cabi_scenarios svc_* order).
	serviceMu.Lock()
	now := time.Now()
	if !lastServiceCall.IsZero() && now.Sub(lastServiceCall) < serviceInfoRateLimit {
		serviceMu.Unlock()
		if response != nil {
			*response = nil
		}
		return 6
	}
	lastServiceCall = now
	serviceMu.Unlock()
	if response == nil {
		return 5
	}
	*response = nil
	if request == nil {
		return 4
	}
	text := goString(request)
	if strings.TrimSpace(text) == "" {
		return 4
	}
	body, code := serviceInfoResponse(text, versionString())
	if code != 0 {
		*response = errString(body)
		return 3
	}
	*response = cString(body)
	return 0
}

func main() {}
