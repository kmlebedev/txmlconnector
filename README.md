# txmlconnector

Go adapter around the proprietary Win32 `TXmlConnector.dll`. The adapter exposes
the connector over gRPC so native Linux and macOS applications do not depend on
Win32 calls or Wine themselves.

## Architecture

```text
Linux/macOS/Windows application
        |
        | gRPC (XML command + broadcast XML stream)
        v
server package (platform-neutral lifecycle and transport)
        |
        | connector.Connector
        v
Win32 adapter (windows/amd64 only, pure Go syscall/stdcall)
        |
        v
TXmlConnector.dll
```

The important boundary is the small `connector.Connector` interface:

```go
type Connector interface {
    Start(MessageHandler) error
    SendCommand(context.Context, string) (string, error)
    Close() error
}
```

This keeps the vendor DLL, its memory ownership and Win32 ABI out of gRPC and
business code. It also makes the server testable on Linux/macOS with a fake
adapter. Calls into the DLL are serialized because the vendor documents the
library as not thread-safe. The callback copies the XML and frees DLL-owned
memory before publishing it to buffered subscribers.

The Win32 executable no longer needs CGO or MinGW. On Linux, run that executable
under Wine and keep Linux clients native. A direct Linux wrapper around the DLL
is not possible because the vendor distributes a Win32 binary, not an ELF shared
library.

## Build and test

```shell
go test ./...
go vet ./...
make server_build     # produces bin/server.exe, CGO_ENABLED=0
```

Run the server on Windows:

```powershell
$env:TC_DLL_PATH = "C:\path\to\txmlconnector64-6.43.2.24.0.dll"
$env:TC_LISTEN_ADDR = ":50051"
.\bin\server.exe
```

Run the same adapter on Linux/macOS using Wine:

```shell
export TC_DLL_PATH=/absolute/path/to/txmlconnector64-6.43.2.24.0.dll
make server
```

Or build and run the container:

```shell
make build
docker run --rm -p 50051:50051 kmlebedev/txmlconnector:local server
```

## Native Go client

The client connects over gRPC and is platform-independent:

```shell
export TC_TARGET=localhost:50051
export TC_LOGIN=...
export TC_PASSWORD=...
export TC_HOST=...
export TC_PORT=...
make client
```

Configuration:

| Variable | Purpose | Default |
| --- | --- | --- |
| `TC_DLL_PATH` | Explicit DLL path | version-derived file name |
| `TC_DLL_VER` | DLL version used in the derived name | `6.43.2.24.0` |
| `TC_DLL_LOG_DIR` | Directory for DLL logs | `logs` |
| `TC_DLL_LOG_LEVEL` | Native DLL log level (`1`-`3`) | `2` |
| `TC_LISTEN_ADDR` | gRPC listen address | `:50051` |
| `TC_TARGET` | Client gRPC target | `localhost:50051` |
| `TC_LOG_LEVEL` | Go application log level | `info` |

## Compatibility notes

- The protobuf service and existing Go client fields remain compatible.
- `server.TxmlSendCommand`, `server.Messages` and `server.Done` remain available
  on `windows/amd64` as deprecated migration shims. New code should depend on
  `connector.Connector` explicitly.
- Every gRPC stream receives its own copy of each XML message. Slow subscribers
  are disconnected with `ResourceExhausted` rather than blocking the vendor
  callback and all other clients.

## References

- [TRANSAQ XML Connector documentation](https://files.comon.ru/usercontent/TXmlConnector.pdf)
- [gRPC](https://grpc.io/)
