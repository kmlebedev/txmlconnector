// Package connector defines the platform-neutral boundary to TRANSAQ XML Connector.
//
// The vendor library is a Win32 DLL. Linux and macOS applications should talk to
// the Windows adapter through the gRPC server instead of importing platform code.
package connector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
)

const (
	DefaultDLLName    = "txmlconnector64"
	DefaultDLLVersion = "6.43.2.24.0"
	DefaultLogDir     = "logs"
	DefaultLogLevel   = 2
)

var (
	ErrNotStarted          = errors.New("TRANSAQ connector is not started")
	ErrAlreadyStarted      = errors.New("TRANSAQ connector is already started")
	ErrUnsupportedPlatform = errors.New("TRANSAQ native connector is unsupported on this platform")
)

// MessageHandler receives an XML message copied from memory owned by the DLL.
// Implementations must return quickly; handlers run on a vendor callback thread.
type MessageHandler func(message string)

// Connector owns the native library lifecycle. Implementations must serialize
// calls because TXmlConnector.dll is not thread-safe.
type Connector interface {
	Start(MessageHandler) error
	SendCommand(context.Context, string) (string, error)
	Close() error
}

// Config contains native adapter settings.
type Config struct {
	DLLPath  string
	LogDir   string
	LogLevel int
}

// ConfigFromEnv keeps the existing TC_* configuration contract. An explicit
// TC_DLL_PATH takes precedence over a version-derived file name.
func ConfigFromEnv() Config {
	cfg := Config{
		DLLPath:  fmt.Sprintf("%s-%s.dll", DefaultDLLName, DefaultDLLVersion),
		LogDir:   DefaultLogDir,
		LogLevel: DefaultLogLevel,
	}
	if version := os.Getenv("TC_DLL_VER"); version != "" {
		cfg.DLLPath = fmt.Sprintf("%s-%s.dll", DefaultDLLName, version)
	}
	if path := os.Getenv("TC_DLL_PATH"); path != "" {
		cfg.DLLPath = path
	}
	if logDir := os.Getenv("TC_DLL_LOG_DIR"); logDir != "" {
		cfg.LogDir = logDir
	}
	if logLevel := os.Getenv("TC_DLL_LOG_LEVEL"); logLevel != "" {
		if parsed, err := strconv.Atoi(logLevel); err == nil {
			cfg.LogLevel = parsed
		}
	}
	return cfg
}

func (c Config) withDefaults() Config {
	if c.DLLPath == "" {
		c.DLLPath = fmt.Sprintf("%s-%s.dll", DefaultDLLName, DefaultDLLVersion)
	}
	if c.LogDir == "" {
		c.LogDir = DefaultLogDir
	}
	if c.LogLevel == 0 {
		c.LogLevel = DefaultLogLevel
	}
	return c
}
