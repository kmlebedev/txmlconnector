//go:build cgo

// Package main builds the drop-in C ABI library over native.Connector. The
// lifecycle texts and codes follow the O reference (docs/protocol/cabi.md,
// cmd/connector/testdata/cabi-reference.json).
package main

/*
#include <stdbool.h>
#include <stdlib.h>

typedef unsigned char BYTE;
typedef bool (*txc_callback)(BYTE*);
typedef bool (*txc_callback_ex)(BYTE*, void*);

static bool txc_invoke(txc_callback f, BYTE* p) { return f(p); }
static bool txc_invoke_ex(txc_callback_ex f, BYTE* p, void* u) { return f(p, u); }
*/
import "C"

import (
	"context"
	"encoding/xml"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

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
	errAlreadyInit = "Не удалось инициализировать библиотеку. Библиотека уже инициализирована"
	errNotInit     = "Библиотека не инициализирована"
	errNotInitCmd  = "Библиотека не инициализована. Необходимо выполнить функцию Initialize()"
)

func errString(text string) *C.uchar { return cString("<error>" + text + "</error>") }

type library struct {
	conn *native.Connector
	msgs chan string
	stop chan struct{}
	wg   sync.WaitGroup
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

func cString(s string) *C.uchar { return (*C.uchar)(unsafe.Pointer(C.CString(s))) }

func freeCString(p *C.uchar) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

func startLocked(logPath string) *C.uchar {
	if logPath != "" {
		_ = os.MkdirAll(logPath, 0o755)
	}
	l := &library{msgs: make(chan string, 1024), stop: make(chan struct{})}
	l.conn = native.New(native.Config{Version: versionString()})
	if err := l.conn.Start(func(message string) {
		select {
		case l.msgs <- message:
		case <-l.stop:
		}
	}); err != nil {
		return errString(err.Error())
	}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		runtime.LockOSThread()
		for {
			select {
			case <-l.stop:
				return
			case message := <-l.msgs:
				cbMu.Lock()
				f, fx, u := cb, cbEx, user
				cbMu.Unlock()
				p := cString(message)
				switch {
				case fx != nil:
					C.txc_invoke_ex(fx, p, u) // return value does not stop delivery
				case f != nil:
					C.txc_invoke(f, p)
				default:
					freeCString(p)
				}
			}
		}
	}()
	lib = l
	return nil
}

//export Initialize
func Initialize(logPath *C.uchar, logLevel C.int) *C.uchar {
	apiMu.Lock()
	defer apiMu.Unlock()
	if lib != nil {
		return errString(errAlreadyInit)
	}
	if logPath == nil {
		return errString("Не удалось инициализировать библиотеку. Не указан путь к директории для хранения лог-файлов")
	}
	if logLevel < 1 || logLevel > 3 {
		return errString("Не удалось инициализировать библиотеку. Wrong log level.")
	}
	return startLocked(C.GoString((*C.char)(unsafe.Pointer(logPath))))
}

//export InitializeEx
func InitializeEx(configXML *C.uchar) *C.uchar {
	apiMu.Lock()
	defer apiMu.Unlock()
	if lib != nil {
		return errString(errAlreadyInit)
	}
	if configXML == nil {
		return errString("Не удалось инициализировать библиотеку. параметр функции равен нулю")
	}
	cfg, msg := parseInitXML(C.GoString((*C.char)(unsafe.Pointer(configXML))))
	if msg != "" {
		return errString(msg)
	}
	return startLocked(cfg.LogPath)
}

//export UnInitialize
func UnInitialize() *C.uchar {
	apiMu.Lock()
	l := lib
	if l == nil {
		apiMu.Unlock()
		return errString(errNotInit)
	}
	lib = nil
	apiMu.Unlock()
	_ = l.conn.Close()
	close(l.stop)
	l.wg.Wait()
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
	text := ""
	if commandXML != nil {
		text = C.GoString((*C.char)(unsafe.Pointer(commandXML)))
	}
	if strings.TrimSpace(text) == "" {
		return errString("Error document empty.")
	}
	if msg := validateCommandXML(text); msg != "" {
		return errString(msg)
	}
	if l == nil {
		return errString(errNotInitCmd)
	}
	result, err := l.conn.SendCommand(context.Background(), text)
	if err != nil {
		return errString(err.Error())
	}
	return cString(result)
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
	if level < 1 || level > 3 {
		return errString("Invalid log level. Should be in range [1,3]")
	}
	return nil
}

//export GetServiceInfo
func GetServiceInfo(request *C.uchar, response **C.uchar) C.int {
	if response == nil {
		return 5
	}
	*response = nil
	if request == nil {
		return 4
	}
	text := C.GoString((*C.char)(unsafe.Pointer(request)))
	if strings.TrimSpace(text) == "" {
		return 4
	}
	serviceMu.Lock()
	now := time.Now()
	if !lastServiceCall.IsZero() && now.Sub(lastServiceCall) < serviceInfoRateLimit {
		serviceMu.Unlock()
		return 6
	}
	lastServiceCall = now
	serviceMu.Unlock()
	body, code := serviceInfoResponse(text, versionString())
	if code != 0 {
		*response = errString(body)
		return 3
	}
	*response = cString(body)
	return 0
}

func main() {}
