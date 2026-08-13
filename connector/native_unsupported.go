//go:build !windows || !amd64

package connector

import "fmt"

// New returns the native adapter on supported systems. The vendor only ships a
// Windows DLL, so Linux and macOS use the Windows gRPC sidecar (usually via Wine).
func New(Config) (Connector, error) {
	return nil, fmt.Errorf("%w: requires windows/amd64; run the server executable with Wine and connect over gRPC", ErrUnsupportedPlatform)
}
