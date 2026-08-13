//go:build windows && amd64

package connector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeConnector struct {
	config Config

	mu              sync.Mutex
	dll             *windows.DLL
	setCallback     *windows.Proc
	sendCommand     *windows.Proc
	freeMemory      *windows.Proc
	initialize      *windows.Proc
	uninitialize    *windows.Proc
	callbackPointer uintptr
	started         bool
	handler         MessageHandler
}

// New constructs a Win32 adapter without loading the DLL. Start performs all
// external work so loading and initialization errors can be returned to callers.
func New(config Config) (Connector, error) {
	config = config.withDefaults()
	if config.LogLevel < 1 || config.LogLevel > 3 {
		return nil, fmt.Errorf("log level must be between 1 and 3, got %d", config.LogLevel)
	}
	return &nativeConnector{config: config}, nil
}

func (c *nativeConnector) Start(handler MessageHandler) error {
	if handler == nil {
		return errors.New("message handler is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return ErrAlreadyStarted
	}
	if err := os.MkdirAll(c.config.LogDir, 0o755); err != nil {
		return fmt.Errorf("create connector log directory: %w", err)
	}

	var err error
	dllPath, err := filepath.Abs(c.config.DLLPath)
	if err != nil {
		return fmt.Errorf("resolve DLL path %q: %w", c.config.DLLPath, err)
	}
	c.dll, err = windows.LoadDLL(dllPath)
	if err != nil {
		return fmt.Errorf("load %q: %w", c.config.DLLPath, err)
	}
	loaded := true
	defer func() {
		if loaded {
			_ = c.dll.Release()
		}
	}()

	if c.setCallback, err = findProc(c.dll, "SetCallback"); err != nil {
		return err
	}
	if c.sendCommand, err = findProc(c.dll, "SendCommand"); err != nil {
		return err
	}
	if c.freeMemory, err = findProc(c.dll, "FreeMemory"); err != nil {
		return err
	}
	if c.initialize, err = findProc(c.dll, "Initialize"); err != nil {
		return err
	}
	if c.uninitialize, err = findProc(c.dll, "UnInitialize"); err != nil {
		return err
	}

	logDir, err := windows.BytePtrFromString(c.config.LogDir)
	if err != nil {
		return fmt.Errorf("encode log directory: %w", err)
	}
	response, _, _ := c.initialize.Call(
		uintptr(unsafe.Pointer(logDir)),
		uintptr(c.config.LogLevel),
	)
	runtime.KeepAlive(logDir)
	if response != 0 {
		return c.takeError("initialize", response)
	}

	c.handler = handler
	c.callbackPointer = windows.NewCallback(func(messagePointer uintptr) (result uintptr) {
		if messagePointer == 0 {
			return 0
		}
		message := bytePtrToString(messagePointer)
		// The callback owns this DLL allocation and must release it before return.
		c.freeMemory.Call(messagePointer)
		defer func() {
			if recover() != nil {
				result = 0
			}
		}()
		c.handler(message)
		return 1
	})

	ok, _, callErr := c.setCallback.Call(c.callbackPointer)
	if ok == 0 {
		response, _, _ = c.uninitialize.Call()
		if response != 0 {
			_ = c.takeError("uninitialize after SetCallback failure", response)
		}
		return callFailure("set callback", callErr)
	}
	c.started = true
	loaded = false
	return nil
}

func (c *nativeConnector) SendCommand(ctx context.Context, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	request, err := windows.BytePtrFromString(command)
	if err != nil {
		return "", fmt.Errorf("encode command: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return "", ErrNotStarted
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	response, _, callErr := c.sendCommand.Call(uintptr(unsafe.Pointer(request)))
	runtime.KeepAlive(request)
	if response == 0 {
		return "", callFailure("send command", callErr)
	}
	message := bytePtrToString(response)
	if ok, _, freeErr := c.freeMemory.Call(response); ok == 0 {
		return "", callFailure("free command response", freeErr)
	}
	return message, nil
}

func (c *nativeConnector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return nil
	}
	c.started = false

	response, _, _ := c.uninitialize.Call()
	var uninitializeErr error
	if response != 0 {
		uninitializeErr = c.takeError("uninitialize", response)
	}
	releaseErr := c.dll.Release()
	if uninitializeErr != nil {
		return uninitializeErr
	}
	if releaseErr != nil {
		return fmt.Errorf("release %q: %w", c.config.DLLPath, releaseErr)
	}
	return nil
}

func (c *nativeConnector) takeError(operation string, pointer uintptr) error {
	message := bytePtrToString(pointer)
	c.freeMemory.Call(pointer)
	return fmt.Errorf("%s: %s", operation, message)
}

func findProc(dll *windows.DLL, name string) (*windows.Proc, error) {
	proc, err := dll.FindProc(name)
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", name, err)
	}
	return proc, nil
}

func callFailure(operation string, callErr error) error {
	if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, callErr)
}

func bytePtrToString(pointer uintptr) string {
	if pointer == 0 {
		return ""
	}
	return windows.BytePtrToString((*byte)(unsafe.Pointer(pointer)))
}
