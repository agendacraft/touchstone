# Touchstone

A layered pull-request review CLI that drives the Droid CLI.

**Early development. Not usable yet.** This is a foundation scaffold, not a
reviewer. `touchstone review` returns an explicit not-implemented error. There
are no review prompts, review orchestration, verifier logic, forge posting,
automatic fixes, telemetry, or releases.

## Requirements

- Go 1.25 or newer to build.
- The [Droid CLI](https://docs.factory.com/droid-cli/overview.md) and a Factory
  account with access to your selected model for agent execution. Model calls
  may incur charges. Follow Factory's authentication instructions; never put
  credentials in `touchstone.toml`.

Building, running `version` or `config validate`, and the test suite do **not**
need Droid, an account, or live model calls. The adapter was probed against
Droid 1.4.1; see [verified capabilities and limits](docs/droid-capabilities.md).

## Build and try the scaffold

```sh
go build -o touchstone ./cmd/touchstone
./touchstone version
./touchstone config validate --cwd .
./touchstone review # exits nonzero: not implemented
```

The default version is `dev`. For a local build label:

```sh
go build -ldflags '-X main.version=local' -o touchstone ./cmd/touchstone
```

## Repository configuration

An optional `touchstone.toml` in the directory passed to `--cwd` overrides
built-in defaults. The command does not search ancestor directories or load
user-wide Touchstone configuration. An absent file uses defaults; an invalid
or unreadable file fails validation. Unknown keys and layer names are errors.

The reserved layers are `correctness`, `security`, and `verification`. All
default to `gpt-6.1-sol`, `medium` reasoning, a five-minute timeout, autonomy
`off`, and read-only intent. These are configurable placeholders, not a
validated model-quality recommendation. No layer runs from the CLI yet.

For example, using only synthetic rules:

```toml
version = 1

[layers.correctness]
timeout = "2m"
rules = ["Explain the observable failure for each proposed defect."]

[[paths]]
pattern = "src/*.go"
[paths.layers.correctness]
reasoning_effort = "high"
rules = ["Consider error-return paths in the synthetic example."]

[[paths]]
pattern = "src/example.go"
[paths.layers.security]
timeout = "3m"
```

Each override accepts `model`, `reasoning_effort`, `timeout`, `autonomy`,
`read_only`, and `rules`. Repository policy must keep `autonomy = "off"` and
`read_only = true`; timeouts must be positive Go duration strings. Model
availability and model-specific reasoning support are checked by Droid at run
time, not by offline config validation.

Precedence is defaults, repository layer settings, then matching path rules.
Paths use Go's slash-separated [`path.Match`](https://pkg.go.dev/path#Match)
globs: `*` does not cross `/`; recursive `**` is rejected. Exact paths outrank
globs. Among globs, more characters other than `*?[]` mean higher precedence.
Equal specificity uses declaration order, with later scalar values winning.
Rules are additive in that order, never silently discarded. Relative,
normalized paths are required.

## Agent and output contracts

The internal `Agent` interface runs one layer per invocation. The Droid adapter
uses prompt and settings files with private permissions, explicit model and
reasoning flags, a bounded subprocess deadline, and a fresh session every time.
It never resumes or forks a previous session. Unix deadlines kill the process
group; other platforms kill the direct child only.

Runs omit `--auto`, restrict tools to `Read,Grep,Glob,LS`, disable hooks, and
disable cloud session sync in per-run settings. Autonomy and
tool restrictions are **not an OS sandbox**. Settings merge with local Droid
settings. Do not run this against untrusted repositories with host credentials.
Using Droid sends prompts and inspected content to the selected model service.

The model's `result` string must pass the
[versioned findings contract](docs/findings.md). Free text is never a report.
Usage is preserved when provided; missing usage and currency cost remain
unknown. Factory credits are not converted to dollars. Process failure,
cancellation, timeout, and malformed output return errors, not clean reviews.
An `incomplete` report is distinct from `clean`. A deterministic fake and
synthetic recorded fixtures let tests run without an account.

## Development

```sh
go build ./...
go vet ./...
go test -race ./...
golangci-lint run ./...
```

CI uses standard GitHub-hosted Ubuntu runners on this public repository, with
no model credentials, paid runner labels, or external review service. The only
runtime dependency is `github.com/pelletier/go-toml/v2`, used for strict TOML
parsing instead of a home-grown parser. CLI parsing, JSON validation, and
subprocess control use the Go standard library.

## License

Apache-2.0. See [LICENSE](LICENSE).
