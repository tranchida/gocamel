---
name: new-component
description: Scaffold a new GoCamel component module under components/<name> with go.mod, go.work entry, 4-pillar skeleton, tests and EN/FR docs
disable-model-invocation: true
argument-hint: <name> [external-library]
---

Create the component `$ARGUMENTS` following the repository rules. Component names are lowercase.

1. **Module**: create `components/<name>/` with its own `go.mod` (`module github.com/tranchida/gocamel/components/<name>`, `go 1.27.0`, requiring `github.com/tranchida/gocamel` at the version used by the sibling components, plus testify). Copy `LICENSE` from a sibling. Never add the external library to the root `go.mod`.
2. **Workspace**: add `./components/<name>` to `go.work` (keep alphabetical order) and run `go mod tidy` in the new module.
3. **Code**: write `<name>_component.go` with the four pillars, modelled on `components/redis/redis_component.go`:
   - `Component.CreateEndpoint` uses `gocamel.ParseURI` and `gocamel.GetConfig*` helpers.
   - `Endpoint` returns a Producer and a Consumer and guards client closure with `sync.Once`.
   - `Producer` fills the exchange `In`/`Out` message; headers use `Camel<Name>*` constants.
   - `Consumer` runs a goroutine that respects `ctx.Done()`, joins on a `sync.WaitGroup` in `Stop()`, and calls `gocamel.ProcessSafely`.
   - Wrap errors with `%w` and context; validate any path derived from headers with `gocamel.ValidatePath*`.
4. **Tests**: `<name>_component_test.go` using testify (`require` for setup, `assert` for outcomes), in-memory fakes where possible, `assert.Eventually` instead of sleeps, `t.Cleanup` for teardown.
5. **Docs (bilingual)**: add the component to `README.md`, `README.fr.md`, and the pages under `hugo-site/content/en/` and `hugo-site/content/fr/`, and add an entry to `CHANGELOG.md` under unreleased.
6. **Verify**: from `components/<name>` run `go build ./... && go vet ./... && go test -race ./...`, then `./check.sh components/<name>`.

All code, comments and messages in English. Use Conventional Commits (`feat(<name>): ...`) if asked to commit.
