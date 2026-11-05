#!/usr/bin/env bash
# Cross-build the C ABI library (cmd/connector) for the release targets with
# Zig as the C toolchain (C9, docs/COMPATIBILITY.md). One host builds all
# targets; Linux is pinned to glibc 2.17 so the .so loads on old distributions.
# macOS targets need the macOS SDK (Go's runtime links libresolv), so they are
# built on a macOS host: the SDK found by xcrun (or $SDKROOT) is passed to zig.
#
# usage: tools/cabi/zig_build.sh OUTDIR [target...]
#   target: linux-amd64 linux-arm64 windows-amd64 macos-arm64 macos-amd64 (default: all)
set -euo pipefail
out=${1:?usage: zig_build.sh OUTDIR [target...]}
shift
targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=(linux-amd64 linux-arm64 windows-amd64 macos-arm64 macos-amd64)
version=${TXC_VERSION:-6.47.2.26.4}
command -v zig >/dev/null || { echo "zig_build: zig not found in PATH" >&2; exit 1; }

for target in "${targets[@]}"; do
	case $target in
	linux-amd64) goos=linux goarch=amd64 triple=x86_64-linux-gnu.2.17 lib=libtxmlconnector.so ;;
	linux-arm64) goos=linux goarch=arm64 triple=aarch64-linux-gnu.2.17 lib=libtxmlconnector.so ;;
	windows-amd64) goos=windows goarch=amd64 triple=x86_64-windows-gnu lib=txmlconnector64.dll ;;
	macos-arm64) goos=darwin goarch=arm64 triple=aarch64-macos lib=libtxmlconnector.dylib ;;
	macos-amd64) goos=darwin goarch=amd64 triple=x86_64-macos lib=libtxmlconnector.dylib ;;
	*) echo "zig_build: unknown target $target" >&2; exit 2 ;;
	esac
	cc="zig cc -target $triple"
	if [ "$goos" = darwin ]; then
		sdk=${SDKROOT:-$(xcrun --show-sdk-path 2>/dev/null || true)}
		[ -n "$sdk" ] || { echo "zig_build: $target needs the macOS SDK (run on macOS or set SDKROOT)" >&2; exit 1; }
		cc="$cc -L$sdk/usr/lib -F$sdk/System/Library/Frameworks"
	fi
	dir="$out/$target"
	mkdir -p "$dir"
	echo "== $target ($triple)"
	CGO_ENABLED=1 GOOS=$goos GOARCH=$goarch CC="$cc" \
		go build -buildmode=c-shared -trimpath -buildvcs=false \
		-ldflags "-s -w -X main.defaultVersion=$version" -o "$dir/$lib" ./cmd/connector
	if [ "$goos" = windows ]; then
		# lld stamps time/GUID fields that differ between identical builds:
		# zero them, then let Go recompute the build ID from the content.
		go run ./tools/cabi/pe-repro "$dir/$lib"
		go tool buildid -w "$dir/$lib"
	fi
	cp cmd/connector/include/txmlconnector.h "$dir/"
	rm -f "$dir/${lib%.*}.h" # the cgo-generated header; txmlconnector.h is the public one
	case $lib in
	*.so | *.dylib) cp "$dir/$lib" "$dir/${lib/libtxmlconnector/libtxmlconnector64}" ;;
	esac
done
