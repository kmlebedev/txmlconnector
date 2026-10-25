# Deploy `txmlconnector` to `palmshell`

The TRANSAQ connector is a Windows/amd64 binary that loads the vendor DLL through
Wine. On `palmshell` it runs as a systemd unit; Docker is not involved. The
Makefile builds `bin/server.exe`, copies the binary and the pinned DLL
atomically, renders the unit, enables and restarts the service, then checks that
it is active and listening.

## Host paths

| Path | Purpose |
|---|---|
| `/root/bin/txmlconnector-server.exe` | server binary |
| `/root/bin/txmlconnector-server.exe.bak` | previous binary for `rollback-staging` |
| `/root/txmlconnector64-6.47.2.26.4.dll` | vendor DLL (pinned) |
| `/root/bin/logs/` | native DLL logs |
| `/etc/systemd/system/txmlconnector-server.service` | systemd unit |
| `/usr/lib/wine/wine64` | Wine runtime |

## Variables

| Variable | Default | Purpose |
|---|---|---|
| `STAGING_HOST` | `palmshell` | SSH host; user must be root |
| `STAGING_BIN_DIR` | `/root/bin` | remote binary directory |
| `STAGING_SERVICE` | `txmlconnector-server.service` | systemd unit name |
| `STAGING_UNIT_PATH` | `/etc/systemd/system/$(STAGING_SERVICE)` | installed unit |
| `STAGING_WINE` | `/usr/lib/wine/wine64` | Wine executable |
| `STAGING_DLL_VER` | `6.47.2.26.4` | DLL version from the repo |
| `STAGING_DLL_PATH` | `/root/txmlconnector64-$(STAGING_DLL_VER).dll` | remote DLL |
| `STAGING_LISTEN_ADDR` | `:50051` | `TC_LISTEN_ADDR` in the unit |
| `STAGING_PORT` | last component of `STAGING_LISTEN_ADDR` | smoke check |
| `STAGING_LOG_DIR` | `$(STAGING_BIN_DIR)/logs` | `TC_DLL_LOG_DIR` |
| `STAGING_SSH` / `STAGING_SCP` | `ssh` / `scp` | transport commands |

## Usage

```bash
make staging-precheck        # SSH, root, Wine, systemd, sha256sum, ss
make deploy-staging          # build + binary + DLL + unit + restart + smoke
make deploy-staging-smoke    # active unit, listening port, startup log line
make deploy-staging-status   # systemd status + listening sockets
make deploy-staging-logs     # last 120 journal lines
make deploy-staging-restart  # restart the installed unit + smoke
make rollback-staging        # restore the previous binary + restart + smoke
```

Individual artifacts:

```bash
make deploy-staging-binary   # build and atomically replace the binary
make deploy-staging-dll      # copy the pinned DLL if its sha256 changed
make deploy-staging-unit     # render and install the unit, daemon-reload
```

`deploy-staging` copies the **Windows** server (`server_build`) and the tracked
DLL from the repository. The binary is built locally; the host does not need Go.
The service runs under Wine `/usr/lib/wine/wine64`.

## Idempotency and rollback

- Binary and DLL are compared by sha256; an unchanged artifact is not copied.
- A new file is copied as `.new`, then `mv` replaces the target, so a running
  Wine process never sees a partially written file. The old binary is kept as
  `.bak` before replacement.
- `rollback-staging` restores `.bak` and restarts the unit. It only rolls back
  the binary; a changed DLL version is rolled back manually.
- `deploy-staging` enables the unit and restarts it explicitly: the service may
  already be running, so `enable --now` alone would not apply a new unit or
  binary.

## Security

The gRPC endpoint has no authentication. The default `:50051` preserves the
current deployment and is reachable over the network; for a non-local bind use a
VPN or an SSH tunnel:

```bash
ssh -N -L 50051:127.0.0.1:50051 palmshell
# then deploy with:
make deploy-staging STAGING_LISTEN_ADDR=127.0.0.1:50051
```

The server stores no TRANSAQ credentials: the client sends `connect` with login,
password, host and port. Do not put credentials in the unit or in the repo.

## Verification

`deploy-staging-smoke` checks:

1. `systemctl is-active txmlconnector-server.service`;
2. `ss -ltn` has the configured port;
3. the journal since the restart contains `server listening on`.

On failure it prints the last journal lines. `make deploy-staging-status` and
`make deploy-staging-logs` are read-only helpers.

The service is independent of the self-hosted GitHub Actions runner on
`palmshell`; deployment does not touch the runner.
