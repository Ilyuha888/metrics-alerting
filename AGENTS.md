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
- `internal/metrics/` — the shared vocabulary: kinds, value types, `Snapshot`, `Batch`, `ErrNotFound`.
  Both storage and handler import it, so neither has to import the other.
- `internal/models/` — `Metrics`, the JSON wire format the agent sends and the server reads and answers;
  `TimestampHeader`, the batch stamp both sides use.
- `internal/config/` — `NetAddress`, the `host:port` flag type both binaries parse, and its default;
  `Seconds`, the bare-seconds interval flag type.
- `internal/logger/` — `New`, the `log/slog` logger both binaries build from their `-log-level` flag;
  the agent's `-l` is the rate limit.
- `internal/retry/` — `Do`, which repeats an operation on a retriable error, and `DefaultDelays`.
- `internal/storage/memstorage/` — `Storage`, the in-memory `handler.Storage`.
- `internal/storage/filestore/` — `Storage`, which wraps `memstorage.Storage` and saves it to a JSON file
  periodically or on every update, and restores it at start.
- `internal/storage/database/` — `Open`, which returns a pinged `*sql.DB` with the embedded goose
  migrations from `migrations/` applied; `IsRetriable` and `IsConnectionLost`, which classify PostgreSQL errors for `retry.Do`.
- `internal/storage/pgstore/` — `Storage`, the `handler.Storage` on PostgreSQL, over the pool from `database`.
- `internal/handler/` — the HTTP layer on chi. It declares the `Storage` and `Pinger`
  interfaces it needs, and passes the request's context to both.
- `internal/agent/` — `Collector` (runtime), `SystemCollector` (gopsutil: memory and CPU load), `Sender`,
  and `Agent`: two pollers, a reporter and `RateLimit` workers that send, all running until the context is done.

Do not create a directory before something goes in it. Git does not track empty
directories, and an empty package is noise.

## Commands

```sh
go build ./cmd/...        # build both binaries
go test ./...             # tests
go vet ./...              # what CI runs, minus the Practicum vettool
gofmt -l -w .             # format
```

Tests in `internal/storage/database` and `internal/storage/pgstore` need a real PostgreSQL. They read a URL-form
DSN from `TEST_DATABASE_DSN`, create and drop their own databases, and skip without it:

```sh
TEST_DATABASE_DSN='postgres://postgres:postgres@localhost:5432/praktikum?sslmode=disable' go test ./...
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
- `iter131` is outside the increment numbering: the increment autotests do not run on it in CI,
  as the workflow's step conditions list branches by name.
- One pull request per increment, added to the sprint's GitHub stack, so the reviewer reviews the sprint at its top PR and the stack merges in one action. Do not merge it; `main` receives an increment only after a reviewer accepts it.
- The first PR of a sprint targets `main`; each next `iterN` PR targets `iter(N-1)`.
- The repo owner merges, with a merge commit, never squash; fix a pushed lower branch with new
  commits only.
- Commit subject only, English, imperative. Add a body when the "Why" is not visible in the
  diff.

## Conventions

- Prefer the standard library. Add a dependency in the sprint that introduces it, not earlier.
- Keep `main` thin: parse flags, build dependencies, call `run() error`. `os.Exit` skips
  deferred calls, so it belongs in `main` and nowhere deeper.
- Errors are returned, not panicked, and wrapped with `fmt.Errorf("...: %w", err)`.
- Comments carry a load-bearing why — an ordering that looks wrong but isn't, a decision
  a reader would otherwise reverse. A comment that restates the code gets deleted. Every
  package keeps its one-line package comment.
- Table-driven tests next to the code as `*_test.go`. The function is named
  `Test<Type>_<Method>_<Expected>`; each case states its own scenario in the subtest name.
  A test file uses the external `<pkg>_test` package unless it needs unexported state.
- statictest's bodyclose check covers tests: close a `rec.Result()` body with
  `defer res.Body.Close()` at the call site; closing it in a helper via `t.Cleanup` fails.
- Package name equals directory name.
- Handle every edge and negative case the increment describes; the autotests tighten each
  sprint.

## Retries

Temporary errors get three more attempts, after pauses of 1, 3 and 5 seconds. A counter update sums,
so a second run of an update that landed would count twice, and which failures repeat depends on the method.

- `database.IsRetriable`: the connection never came up (57P03, a `*pgconn.ConnectError` without a server
  code) or the server reports a connection exception (class 08). 08007 is excluded, since the commit's
  result is unknown. Not 40P01, 53300 or a context error during a statement.
- `database.IsConnectionLost`: 57P01 or `driver: bad connection`, a connection that was up and went away.
  It repeats only where a repeat cannot add a counter twice.
- `pgstore` by method: `SetGauge`, `Gauge`, `Counter` and `Snapshot` repeat on both. `AddCounter` repeats
  on `IsRetriable` only. `UpdateBatch` repeats on `IsRetriable`, and on a lost connection only if `BeginTx`
  failed.
- `pgstore` attempts: each has its own `queryTimeout`; a pause waits on the caller's context. `UpdateBatch`
  and `Snapshot` repeat the whole transaction from `BeginTx`.
- `database.Open`: the ping repeats inside the caller's context. Migrations do not repeat.
- `GET /ping` does not repeat: it reports the state now.
- Agent, `Sender`: only a `*net.OpError` with `Op` `dial`. Not EOF, a timeout or a non-200 answer.

## Stopping the agent

The agent runs four kinds of goroutine: a runtime poller and a system poller, each on `pollInterval`; a reporter
on its own `reportInterval` ticker, which takes one snapshot and hands it to a worker over an unbuffered channel;
and `RateLimit` workers, each sending one snapshot at a time. While every worker is busy, the reporter waits and
its ticker drops the ticks it misses. A snapshot takes the PollCount delta from the collector; a failed send
puts it back with `Restore`, so no poll is in two snapshots at once, however many sends are in flight.

SIGINT or SIGTERM cancels the context `Run` got from `main`. The signal interrupts pauses only: a request
in flight waits for its answer, up to the client's 5 s timeout, since a cut request may have landed and a
repeat would count `PollCount` twice.

- The pollers and the reporter stop. A snapshot the reporter held for a worker goes back to the collector. The
  reporter closes the channel, and `Run` waits until every worker has finished its send.
- No send in flight at the signal ended in an error: one last report goes out with everything left, restored
  deltas included, on its own context of 5 s. That context stops only its pauses, so a request started at 4 s
  can hold it to 9 s.
- A send in flight at the signal ended in an error: no last report, and the polls not yet delivered are lost.
- A pause the signal cut is not logged as a `send metrics` error. A real failure is.
- Then `agent stopped` at Info, `Run` returns, the process exits 0.

## Batch order

Several workers send at once, so batches can reach the server out of order. The reporter stamps each
snapshot with the time it took it, and `Sender` puts that time in `X-Metrics-Timestamp` as Unix
nanoseconds, the same value on every attempt. A zero time sends no header.

On `POST /updates/` with the header, the handler keeps, per gauge, the stamp of the batch that last wrote
it. Under one lock it drops the gauges whose stamp is not newer than the stored one, writes the rest with
`UpdateBatch`, and records their stamps only if the write succeeded. Counters are always added.

- No header (the autotests, `/update/`, `POST /update`): stored as before; no check, no stamp recorded.
- A header that is not a decimal integer: 400, nothing stored.
- The stamp counts only if the request passed a `HashSHA256` check (on a server with a key) and is at most
  `maxStampLead`, 1 minute, ahead of the server clock. Otherwise the batch is stored as without the header,
  and a Warn is logged: an unchecked or far-future stamp would skip every later batch's gauges.
- Stamps live in the handler's memory only. The storages do not know them, and a restarted server takes
  the first stamped batch as is.
- The `HashSHA256` signature covers the body, not this header.

## Known debt

- The server does not save on SIGINT or SIGTERM, so metrics newer than the last save are
  lost when it stops. Saving on shutdown needs signal handling and a graceful
  `Shutdown`, which wait for the sprint on goroutines.

## Updating the template

The upstream template is wired as the `template` remote. To pull newer autotests:

```sh
git fetch template && git checkout template/v2 .github
```
