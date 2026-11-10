// Package connector is a deprecated alias of
// github.com/kmlebedev/txmlconnector-native/connector: the platform-neutral
// connector.Connector boundary and the Win32 adapter of the vendor DLL moved
// there on 2026-11-10 together with the native backend.
//
// Deprecated: import github.com/kmlebedev/txmlconnector-native/connector.
package connector

import nc "github.com/kmlebedev/txmlconnector-native/connector"

const (
	DefaultDLLName    = nc.DefaultDLLName
	DefaultDLLVersion = nc.DefaultDLLVersion
	DefaultLogDir     = nc.DefaultLogDir
	DefaultLogLevel   = nc.DefaultLogLevel
)

// The errors are the same values, so errors.Is works across both packages.
var (
	ErrNotStarted          = nc.ErrNotStarted
	ErrAlreadyStarted      = nc.ErrAlreadyStarted
	ErrUnsupportedPlatform = nc.ErrUnsupportedPlatform
)

type (
	MessageHandler = nc.MessageHandler
	Connector      = nc.Connector
	Config         = nc.Config
)

// ConfigFromEnv is nc.ConfigFromEnv.
func ConfigFromEnv() Config { return nc.ConfigFromEnv() }

// New is nc.New: the Win32 DLL adapter on windows/amd64, ErrUnsupportedPlatform elsewhere.
func New(config Config) (Connector, error) { return nc.New(config) }
