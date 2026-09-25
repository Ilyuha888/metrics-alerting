# AGENTS.md

Metrics collection and alerting service for the Yandex Practicum "Go advanced" course,
built from the `go-musthave-metrics-tpl` template. Two binaries: a server that receives
and stores runtime metrics over HTTP, and an agent that polls `runtime` and reports to
the server.

Module path is `github.com/Ilyuha888/metrics-alerting`.

## Layout

- `cmd/server/` — server entry point. Directory name is fixed by CI.
- `cmd/agent/` — agent entry point. Directory name is fixed by CI.
- `internal/` — everything the two binaries share. A `main` package cannot be imported,
  so any code used by both lives here.
- `internal/metrics/` — the shared vocabulary: kinds, value types, `Snapshot`, `ErrNotFound`.
  Both storage and handler import it, so neither has to import the other.
- `internal/storage/` — `MemStorage`.
- `internal/handler/` — the HTTP layer on chi. It declares the `Storage` interface it needs.
- `internal/agent/` — collector, sender and the polling loop.

Do not create a directory before something goes in it. Git does not track empty
directories, and an empty package is noise.

## Commands

```sh
go build ./cmd/...        # build both binaries
go test ./...             # tests
go vet ./...              # what CI runs, minus the Practicum vettool
gofmt -l -w .             # format
```

CI builds each binary from inside its own directory, so the artifacts are
`cmd/server/server` and `cmd/agent/agent`. Both are gitignored.

## CI

Two workflows, both triggered by any pull request and by pushes to `main`.

- `statictest.yml` — `go vet` with the Practicum `statictest` vettool. Must be green.
- `mertricstest.yml` — the increment autotests. A `branchtest` job rejects any branch
  whose name does not match `iter<number>`, unless the ref is `main`. Increment N's tests
  run on branches `iterN` and above, so later branches re-run every earlier increment.

There are 14 increments, so branches run `iter1` through `iter14`. What each step feeds
`metricstest` constrains the code:

- `-binary-path=cmd/server/server`, `-agent-binary-path=cmd/agent/agent` — fixed paths.
- `-server-port` from increment 4 on, a random free port per step. A hardcoded `:8080`
  passes increments 1-3 and fails from 4 onward.
- `-source-path=.` from increment 2 on — some checks read the source tree, not just the
  running binary.
- `-file-storage-path` at increment 9, `-database-dsn` against the job's Postgres service
  from increment 10 on, `-key` at increment 14.
- Increment 14 also runs `go test -v -race ./...`, so the repo's own tests must exist and
  be race-clean by then.

Autotest sources: https://github.com/Yandex-Practicum/go-autotests

## Git

- One branch per increment, named `iterN`. Branch increment N+1 off increment N, not off
  `main`.
- One pull request per increment. Do not merge it; `main` receives an increment only after
  a reviewer accepts it.
- Commit subject only, English, imperative. Add a body when the why is not visible in the
  diff.

## Conventions

- Standard library only. Add a dependency in the sprint that introduces it, not earlier.
- Keep `main` thin: parse flags, build dependencies, call `run() error`. `os.Exit` skips
  deferred calls, so it belongs in `main` and nowhere deeper.
- Errors are returned, not panicked, and wrapped with `fmt.Errorf("...: %w", err)`.
- Comments carry a load-bearing why — an ordering that looks wrong but isn't, a decision
  a reader would otherwise reverse. A comment that restates the code gets deleted. Every
  package keeps its one-line package comment.
- Table-driven tests next to the code as `*_test.go`. The function is named
  `Test<Type>_<Method>_<Expected>`; each case states its own scenario in the subtest name.
  A test file uses the external `<pkg>_test` package unless it needs unexported state.
- Package name equals directory name.
- Handle every edge and negative case the increment describes; the autotests tighten each
  sprint.

## Known debt

- `MemStorage` has no lock, so concurrent requests — two reports, or a report and a read —
  race and crash the process with a `concurrent map` fatal error. Deliberate: the mutex lands in the sprint that teaches it.
  Increment 14 runs `go test -race`, so it has to be gone by then.
- The listen address is hardcoded. Increment 4 hands the server a random port, so it moves
  behind a flag there.

## Updating the template

The upstream template is wired as the `template` remote. To pull newer autotests:

```sh
git fetch template && git checkout template/v2 .github
```
