//go:build cgo

// Package main builds the drop-in C ABI library (libtxmlconnector.so/.dylib/
// txmlconnector64.dll) over native.Connector. The C signatures are the 9 exports
// of txmlconnector64-6.47.2.26.4.dll; exact wrapper texts/codes for
// InitializeEx/GetServiceInfo/SetLogLevel are pending RE-2 (task-70, marked I/TODO).
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
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/kmlebedev/txmlconnector/native"
)

// version is the drop-in DLL version; override with TXC_VERSION or
// -ldflags "-X main.defaultVersion=...".
var defaultVersion = "6.47.2.26.4"

func versionString() string {
	if v := os.Getenv("TXC_VERSION"); v != "" {
		return v
	}
	return defaultVersion
}

type library struct {
	conn *native.Connector
	msgs chan string
	stop chan struct{}
	wg   sync.WaitGroup

	mu       sync.Mutex
	cb       C.txc_callback
	cbEx     C.txc_callback_ex
	user     unsafe.Pointer
	logLevel int
	logPath  string
}

var (
	apiMu sync.Mutex // serializes Initialize/UnInitialize and the globals
	lib   *library
)

func cString(s string) *C.uchar {
	return (*C.uchar)(unsafe.Pointer(C.CString(s)))
}

// freeCString is the inverse of C.CString for callback payloads and command
// results; exposed as FreeMemory to C clients.
func freeCString(p *C.uchar) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

// startLocked creates the connector and the single callback thread. apiMu is
// held by the caller.
func startLocked(logPath string, logLevel int) *C.uchar {
	l := &library{
		msgs:     make(chan string, 1024),
		stop:     make(chan struct{}),
		logPath:  logPath,
		logLevel: logLevel,
	}
	l.conn = native.New(native.Config{Version: versionString()})
	if err := l.conn.Start(func(message string) {
		select {
		case l.msgs <- message:
		case <-l.stop:
		}
	}); err != nil {
		return cString(err.Error())
	}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		runtime.LockOSThread() // one callback OS thread, like the DLL
		for {
			select {
			case <-l.stop:
				return
			case message := <-l.msgs:
				l.mu.Lock()
				f, fx, u := l.cb, l.cbEx, l.user
				l.mu.Unlock()
				p := cString(message) // the client frees it via FreeMemory
				switch {
				case fx != nil:
					C.txc_invoke_ex(fx, p, u)
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
		// TODO(RE-2 task-70): exact DLL text for a second Initialize.
		return cString("Already initialized (I)")
	}
	path := ""
	if logPath != nil {
		path = C.GoString((*C.char)(unsafe.Pointer(logPath)))
	}
	return startLocked(path, int(logLevel))
}

//export InitializeEx
func InitializeEx(configXML *C.uchar) *C.uchar {
	apiMu.Lock()
	defer apiMu.Unlock()
	if lib != nil {
		return cString("Already initialized (I)")
	}
	if configXML == nil {
		return cString("Empty initialization XML (I)")
	}
	cfg, err := parseInitXML(C.GoString((*C.char)(unsafe.Pointer(configXML))))
	if err != nil {
		return cString(err.Error())
	}
	return startLocked(cfg.LogPath, cfg.LogLevel)
}

//export UnInitialize
func UnInitialize() *C.uchar {
	apiMu.Lock()
	l := lib
	lib = nil
	apiMu.Unlock()
	if l == nil {
		return nil
	}
	_ = l.conn.Close()
	close(l.stop)
	l.wg.Wait()
	return nil
}

//export SetCallback
func SetCallback(f C.txc_callback) C.bool {
	apiMu.Lock()
	defer apiMu.Unlock()
	if lib == nil {
		return false
	}
	lib.mu.Lock()
	lib.cb, lib.cbEx, lib.user = f, nil, nil
	lib.mu.Unlock()
	return true
}

//export SetCallbackEx
func SetCallbackEx(f C.txc_callback_ex, userData unsafe.Pointer) C.bool {
	apiMu.Lock()
	defer apiMu.Unlock()
	if lib == nil {
		return false
	}
	lib.mu.Lock()
	lib.cb, lib.cbEx, lib.user = nil, f, userData
	lib.mu.Unlock()
	return true
}

//export SendCommand
func SendCommand(commandXML *C.uchar) *C.uchar {
	apiMu.Lock()
	l := lib
	apiMu.Unlock()
	if l == nil {
		// TODO(RE-2 task-70): exact DLL <error> text/code for use before Initialize.
		return cString("<error>Library is not initialized (I)</error>")
	}
	if commandXML == nil {
		return cString("<error>Empty command (I)</error>")
	}
	result, err := l.conn.SendCommand(context.Background(), C.GoString((*C.char)(unsafe.Pointer(commandXML))))
	if err != nil {
		return cString("<error>" + xmlEscape(err.Error()) + "</error>")
	}
	return cString(result)
}

//export FreeMemory
func FreeMemory(p *C.uchar) C.bool {
	if p == nil {
		return false
	}
	C.free(unsafe.Pointer(p))
	return true
}

//export SetLogLevel
func SetLogLevel(level C.int) *C.uchar {
	apiMu.Lock()
	defer apiMu.Unlock()
	if level < 1 || level > 3 {
		// TODO(RE-2 task-70): exact DLL text is "Invalid log level. Should be in
		// range [%d,%d]" (D); the range values are pending RE-2.
		return cString(fmt.Sprintf("Invalid log level. Should be in range [1,3] (I, requested %d)", int(level)))
	}
	if lib != nil {
		lib.logLevel = int(level)
	}
	return nil
}

//export GetServiceInfo
func GetServiceInfo(request *C.uchar, response **C.uchar) C.int {
	if response == nil {
		return 4 // TODO(RE-2 task-70): NULL response code is D/I.
	}
	*response = nil
	if request == nil {
		return 4
	}
	req, err := parseServiceInfoRequest(C.GoString((*C.char)(unsafe.Pointer(request))))
	if err != nil {
		return 2
	}
	info, ok := serviceInfoValue(req.Value, versionString())
	if !ok {
		return 3
	}
	*response = cString(info)
	return 0
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func main() {}
