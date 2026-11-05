//go:build !windows

package dlllog

// consoleEOL is "\n" outside Windows, where stdout has no text mode.
const consoleEOL = "\n"
