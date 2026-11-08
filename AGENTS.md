# nps

nps is the server and npc the client of a tunnel: npc connects out to nps,
and nps forwards visitors through that connection to targets npc reaches.

## Layout

- `cmd/nps`, `cmd/npc`: the commands. Only they decide exit codes; library
  code returns errors and never calls `os.Exit` or panics in `init`.
- `server`: the running server, `server.New(...).Start(ctx)` and `Close()`.
  It owns the bridge, the running tunnels and the web manager; its
  goroutines and resources run in a `lifecycle.Scope`.
  - `server/httpproxy`: the host (domain) proxy; `server/connection`:
    listeners and port sharing; `server/ports`: tunnel ports;
    `server/stats`: dashboard samples.
- `bridge`: where npc connects; `bridge/legacy` is the old wire protocol.
- `client`: npc. `client/launch` builds npc from a launch spec.
- `web`: `routers` (HTTP), `api` (handlers), `service` (logic, reaches the
  running server only through the `Runtime` interface), `ui`, `views`,
  `static`.
- `lib/model`: clients, tunnels, hosts, targets, flows, limits.
  `lib/file`: the JSON store of them. Other `lib/*` are small shared
  packages, one subject each (`netaddr`, `auth`, `conn`, `proxy`, ...).
- `internal/tunnelwire`: codec of the old protocol, standard library only.

## Rules the tests keep

- `lib/`, `client/`, `cmd/npc/` and `bridge/legacy/` do not import
  `lib/file` (the store) or `server/...`: npc carries neither
  (`lib/model/boundary_test.go`). `bridge` and `server/httpproxy` do not
  import `lib/file` either: they reach the store through the small `Store`
  interface each defines, which `server.New` passes them.
- Only `bridge`, `client`, `cmd` and `server/connection/portmux.go` import
  `bridge/legacy`; only `bridge/legacy` and a few listed files import
  `internal/tunnelwire` (their `boundary_test.go`).
- Every package with tests has `main_test.go` with
  `func TestMain(m *testing.M) { lifecycletest.VerifyMain(m) }`, which fails the
  package when tests leave goroutines blocked for good.
- The store has no package state: `cmd/nps` opens it (`file.Open`) and
  passes it to `server.New` and `routers.Init`; tests build their own with
  `file.New(dir)` (`TestStoreHasNoPackageState`).

## Contracts with later major versions

- `npc install` writes the connection settings to `<install dir>/conf/npc-service.json`
  (or `conf/npc-service-launch-N.conf` for `--launch` input) and the service
  passes `--launch=@<that file>`. Later npc versions look next to that file
  for their migration file, so these names and locations do not change.
- nps and npc translate command-line arguments of earlier versions or
  ignore them with a warning, by the rules in `internal/cliargs/rules.go`,
  and ignore other unknown ones with a warning, so that a service installed
  by an older version still starts after an update. Old npc.conf and
  nps.conf keys are handled in `lib/config/legacy.go` and
  `lib/servercfg/legacy.go`.
- Point-to-point access (tunnel mode `secret`) names its visitor settings
  as the later major version does: `local_type` (`tcp`, `udp`, `tcp_udp`,
  `mixed`, `transparent`) and `conn_mode` (`auto`, `relay`, `direct`).
  Settings of earlier versions are converted only in the legacy files
  (`lib/config/legacy.go`, `lib/file/legacy.go`, `legacyBundle` in
  `web/service`) and `internal/cliargs/rules.go`, to be removed together
  in the next major version.

- A server candidate (`-server`, `server_addr`; `lib/connprofile`) is
  written as the carrier entries of the later major version: URL schemes
  `tcp`, `tls`, `quic`, `http`, `https` and the parameters `tier`, `weight`,
  `standby`, `count`, `dirs`, `kinds`, `host`, `sni`, with the same names
  and meanings, and `connprofile.Candidate` has their fields. Only `alpn`
  and the schemes `kcp`, `ws`, `wss` are of this version.

## Adding a tunnel or host setting

1. Add the field to `lib/model` and one row to `TunnelSettings` or
   `HostSettings` in `lib/model/settings.go` (form parameter, npc.conf key,
   options). Settings marked `Custom` are applied in `web/service`.
2. Add it to the add/edit templates in `web/views`, to
   `web/static/page/languages.xml`, and to `docs/reference/web-api.md` and
   `docs/reference/npc-config.md`.

`lib/model/settings_test.go`, the bridge config tests and
`web/ui/languages_test.go` fail when a layer is missed. `Update` copies every
field of an edit except those `TestUpdateCopiesFields` lists as kept.

## Commands

```bash
go vet ./...
go test ./... -count=1 -timeout 400s
go test ./bridge ./server/... ./client ./lib/proxy -race -count=1 -timeout 500s
GOOS=windows go vet ./...
```

Build scripts live in `scripts/`; `install.sh` stays at the root, where
releases are installed from.
