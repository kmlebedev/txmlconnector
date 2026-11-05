//go:build !cgo

// Package main's C ABI needs cgo. This stub keeps "go build ./..." (and the
// pure-Go cross builds) working when CGO is disabled; the real library is built
// with -buildmode=c-shared and CGO_ENABLED=1.
package main

func main() {}
