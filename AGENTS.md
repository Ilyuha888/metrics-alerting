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
- `internal/models/` — domain types. No business logic.

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

The autotest binaries take `-binary-path=cmd/server/server` and
`-agent-binary-path=cmd/agent/agent`. Those paths are not negotiable.

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
- Every exported identifier has a doc comment. Comments explain why, not what.
- Table-driven tests next to the code as `*_test.go`, named
  `Test<Function>_<Scenario>_<Expected>`.
- Package name equals directory name.
- Handle every edge and negative case the increment describes; the autotests tighten each
  sprint.

## Updating the template

The upstream template is wired as the `template` remote. To pull newer autotests:

```sh
git fetch template && git checkout template/v2 .github
```
