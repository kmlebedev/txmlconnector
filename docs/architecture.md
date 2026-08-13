# Architecture

## Context

TRANSAQ distributes its native connector as a 64-bit Win32 DLL. It exports a C
ABI using `stdcall`, but it is not a portable C library: Linux cannot load the
binary directly. Wine can host a Windows process that loads it.

The DLL has two constraints that shape this design:

1. calls are not thread-safe and must be serialized;
2. its callback must return quickly and must not invoke lifecycle or command
   functions because that can deadlock the library.

## Components

- `connector`: a small port owned by the application. Its Win32 adapter loads
  and validates all required procedures, owns `Initialize`/`UnInitialize`,
  serializes commands, copies callback data and releases DLL-owned memory.
- `server`: application lifecycle plus a gRPC transport adapter. It contains no
  Win32 code and accepts an injected `connector.Connector` for tests.
- `server.broker`: non-blocking fan-out. Every gRPC subscriber sees every XML
  message. A slow subscriber is detached before it can block the DLL callback.
- `client`: native cross-platform gRPC client and XML message router.
- `client/commands`: protocol data structures and XML command encoding.

Dependencies point inward:

```text
commands <- client <- Linux/macOS/Windows applications
                         |
                         | gRPC
                         v
proto <- server -> connector interface <- Win32 adapter -> DLL
```

## Lifecycle

1. The process creates a connector from configuration.
2. The server starts the connector and registers a short callback.
3. A gRPC client opens its response stream and waits for response headers.
4. Only after stream readiness does the client send the `connect` command. This
   prevents the initial securities and account snapshots from being lost.
5. Commands are serialized in the Win32 adapter.
6. On cancellation the server stops accepting streams, drains active RPCs,
   calls `UnInitialize` exactly once and releases the DLL.

## Failure policy

- DLL load, missing symbol and initialization failures are returned from
  `Start`; package initialization never terminates the process.
- A panic in an application callback is contained at the Win32 boundary and is
  reported to the DLL as a failed callback instead of crossing foreign code.
- A nil or failed native command response becomes a gRPC `Internal` error.
- One slow stream receives `ResourceExhausted`; healthy streams continue.
- gRPC shutdown is graceful up to a bounded timeout, then forced.
- Unsupported platforms return an explicit error that points to the Wine/gRPC
  deployment instead of failing through build constraints.

## Migration path

The protobuf contract remains unchanged. Older Windows code using
`server.TxmlSendCommand`, `server.Messages` or `server.Done` can migrate
incrementally; deprecated shims remain on `windows/amd64`. New code should own a
`connector.Connector` explicitly or use the gRPC client.

If TRANSAQ publishes a native Linux `.so` in the future, implement another file
inside `connector` behind Linux build tags. Neither the gRPC service nor clients
need to change.
