# nps

nps is the server and npc the client of a tunnel: npc connects out to nps,
and nps forwards visitors through that connection to targets npc reaches.

## Rules the tests keep

- Only the commands in `cmd/` decide exit codes; library code returns errors
  and never calls `os.Exit` or panics in `init`.
- `lib/`, `client/`, `cmd/npc/` and `bridge/legacy/` do not import `lib/file`
  (the store) or `server/...`: npc carries neither. `bridge` and
  `server/httpproxy` reach the store through the small `Store` interface each
  defines, which `server.New` passes them.
- `web/service` reaches the running server only through its `Runtime`
  interface.
- The import limits on `bridge/legacy` and `internal/tunnelwire` are listed in
  their `boundary_test.go`.
- Every package with tests has `main_test.go` with
  `func TestMain(m *testing.M) { lifecycletest.VerifyMain(m) }`, which fails the
  package when tests leave goroutines blocked for good.
- The store has no package state: `cmd/nps` opens it and passes it on; tests
  build their own with `file.New(dir)`.
- The vectors in `client/legacy/testdata/wire` record what released clients
  depend on; do not update them to match a change.

## Contracts with later versions

- `npc install` writes its connection settings to
  `<install dir>/conf/npc-service*`. Later versions look next to those files
  for their migration file, so these names and locations do not change.
- Settings of earlier versions are converted or ignored with a warning only in
  `internal/cliargs/rules.go` and the `legacy.go` files, so that a service
  installed by an older version still starts after an update.

## Adding a tunnel or host setting

Add the field to `lib/model` and a row to its settings table in
`lib/model/settings.go`, then to the web forms, the language file and the
reference docs. `lib/model/settings_test.go`, the bridge config tests and
`web/ui/languages_test.go` fail when a layer is missed.

## Commands

```bash
go vet ./...
go test ./... -count=1 -timeout 400s
go test ./bridge ./server/... ./client ./lib/proxy -race -count=1 -timeout 500s
GOOS=windows go vet ./...
```
