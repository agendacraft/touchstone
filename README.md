# Touchstone

A layered pull-request review CLI that drives the Droid CLI.

**Early development. Findings are unverified.** `touchstone review` runs
independent correctness and security specialists over a supplied diff. There
is no verifier, deduplication, git/PR input, forge posting, automatic fixing,
telemetry, or release yet. Schema validity does not establish review quality.

## Requirements

- Go 1.25 or newer to build.
- The [Droid CLI](https://docs.factory.com/droid-cli/overview.md) and a Factory
  account with access to your selected model for agent execution. Model calls
  may incur charges. Follow Factory's authentication instructions; never put
  credentials in `touchstone.toml`.

Building, running `version` or `config validate`, and the default test suite do
**not** need Droid, an account, or live model calls. The adapter was probed against
Droid 1.4.1; see [verified capabilities and limits](docs/droid-capabilities.md).

## Build and review

```sh
go build -o /tmp/touchstone ./cmd/touchstone
/tmp/touchstone version
/tmp/touchstone config validate --cwd .
# Model calls below require Droid authentication and may incur charges.
/tmp/touchstone review --diff /tmp/synthetic.patch --cwd .
/tmp/touchstone review --diff - --concurrency 1 < /tmp/synthetic.patch
```

Supply a nonempty unified diff, up to 16 MiB. `--diff` is required; `-` reads
stdin. File paths resolve relative to the shell's current directory. `--cwd`
selects the directory containing optional `touchstone.toml`, not the diff
file's directory. No git command runs. The agent works in a private scratch
directory containing only the supplied diff, not in the configured repository.
It has no repository context beyond the diff. Synthetic examples only belong
in this public repository; do not commit private review inputs or outputs.

Both specialist layers run concurrently by default; `--concurrency N` bounds
active layers (positive integer). Each layer gets its own configured timeout,
starting after it acquires a worker slot. One fresh-session repair retry for
malformed model text shares that same timeout. Transport errors, process
failures, cancellation, and timeouts are not retried. A failed layer always
appears as `incomplete`, while successful peers' findings remain available.

Stdout contains one JSON report. Exit status: **0** clean, **1** findings,
**2** incomplete (even if some findings exist), **3** command/setup/output
error. Help/version/config validation also exit 0 on success. Diagnostics use
stderr, not stdout. See the stable [review-output contract](docs/review-output.md)
for downstream tooling and future evals.

The default version is `dev`. For a local build label:

```sh
go build -ldflags '-X main.version=local' -o /tmp/touchstone ./cmd/touchstone
```

## Repository configuration

An optional `touchstone.toml` in the directory passed to `--cwd` overrides
built-in defaults. The command does not search ancestor directories or load
user-wide Touchstone configuration. An absent file uses defaults; an invalid
or unreadable file fails validation. Unknown keys and layer names are errors.

The reserved layers are `correctness`, `security`, and `verification`. All
default to `glm-5.3-flash`, `low` reasoning, a five-minute timeout, autonomy
`off`, and read-only intent. These are configurable defaults, not a validated
model-quality recommendation. Only correctness and security run in S1;
verification settings are reserved and do not cause a verifier to run.

Set `model` per layer in `touchstone.toml`; there is no
`--model` CLI flag. Model and reasoning settings override
independently. The [GLM capability trial](docs/droid-capabilities.md#glm-53-flash-follow-up)
accepted 5/5 outputs as valid schema, but only 4/5 fully matched the requested
payload. Schema validity is not evidence of review quality or instruction
reliability.

For example, using only synthetic rules:

```toml
version = 1

[layers.correctness]
model = "glm-5.3-flash"
reasoning_effort = "low"
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
model = "gpt-6.1-sol"
reasoning_effort = "high"
timeout = "3m"
```

Each override accepts `model`, `reasoning_effort`, `timeout`, `autonomy`,
`read_only`, and `rules`. Repository policy must keep `autonomy = "off"` and
`read_only = true`; timeouts must be positive Go duration strings. Model
availability and model-specific reasoning support are checked by Droid at run
time, not by offline config validation.

The diff runner uses only repository-wide layer settings and additive `rules`
in its embedded specialist prompts. Path-based review selection is not part
of S1: the config resolver validates and supports the rules below, but `review`
does **not** apply them or infer paths from a diff.

Resolver precedence is defaults, repository layer settings, then matching path
rules.
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

The review runner validates a findings array in the model's `result` string.
A single bare or `json` code fence is allowed; surrounding prose, malformed
JSON, unknown fields, and duplicate keys are rejected. A second malformed
response makes that layer incomplete. The foundation's strict
[legacy report contract](docs/findings.md) remains available to adapter callers;
the CLI uses the [review-output contract](docs/review-output.md) instead.
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

An optional real headless Droid smoke test uses only a small synthetic diff:

```sh
TOUCHSTONE_LIVE_SMOKE=1 go test -v ./internal/review -run '^TestLiveDroidSmoke$' -count=1
```

It runs both default specialist layers in fresh sessions, with a three-minute
overall deadline. It needs local Droid authentication, sends the synthetic diff
to the model service, and may incur charges (including one repair per layer).
It checks transport and schema completion, not review accuracy. Normal tests
skip it; CI needs no model credentials.

CI uses standard GitHub-hosted Ubuntu runners on this public repository, with
no model credentials, paid runner labels, or external review service. The only
runtime dependency is `github.com/pelletier/go-toml/v2`, used for strict TOML
parsing instead of a home-grown parser. CLI parsing, JSON validation, and
subprocess control use the Go standard library.

## License

Apache-2.0. See [LICENSE](LICENSE).

Copyright 2026 Dela Anthonio
