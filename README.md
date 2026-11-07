# Touchstone

A layered pull-request review CLI that drives the Droid CLI.

**Early development.** `touchstone review` runs independent correctness and
security specialists over a supplied diff, then a fresh skeptical verifier.
Confirmed findings stay, rejected claims move to an audit section, and uncertain
findings stay with a lower-rank label. Cross-layer triage marks possible
duplicates and ranks each claim before optional filtering.
Git repositories and diff files are supported, with path-specific policy and
opt-in local deterministic checks. Terminal, JSON, and Markdown output are
available. There is no PR input, forge posting, automatic fixing, or telemetry.
Verification is model judgment, not proof of correctness.

## Install

Install a released binary without root or a package manager. The script
downloads the archive for your platform, verifies its SHA-256 checksum against
the release's `checksums.txt`, and installs into `~/.local/bin` (override with
`--install-dir` or `TOUCHSTONE_INSTALL_DIR`):

```sh
# Pinned to a release tag: replace vX.Y.Z with the latest tag from the
# releases page. Pinning keeps the script and binaries they install aligned.
curl -fsSL https://raw.githubusercontent.com/agendacraft/touchstone/vX.Y.Z/install.sh | sh -s -- --version vX.Y.Z
# Or the latest script from main:
curl -fsSL https://raw.githubusercontent.com/agendacraft/touchstone/main/install.sh | sh
```

Piping a URL to `sh` runs downloaded code directly, so read
[install.sh](install.sh) before piping it, or download it first and run
`sh install.sh`. Pin a version with `--version v0.1.0` or `TOUCHSTONE_VERSION`;
preview with `--dry-run`; uninstall with `rm ~/.local/bin/touchstone`.

With Go 1.25 or newer:

```sh
go install github.com/agendacraft/touchstone/cmd/touchstone@latest
```

To download a release manually, fetch the archive for your platform and the
release's `checksums.txt`, verify, then unpack:

```sh
version=vX.Y.Z
os=linux arch=amd64 # or darwin with arm64 or amd64
curl -fsSLO "https://github.com/agendacraft/touchstone/releases/download/${version}/touchstone_${version}_${os}_${arch}.tar.gz"
curl -fsSLO "https://github.com/agendacraft/touchstone/releases/download/${version}/checksums.txt"
grep "touchstone_${version}_${os}_${arch}.tar.gz" checksums.txt | sha256sum -c - # shasum -a 256 -c - on macOS
tar -xzf "touchstone_${version}_${os}_${arch}.tar.gz"
```

`touchstone version` reports the installed version.

## Requirements

- Go 1.25 or newer to build.
- Git for repository inputs (no network fetch is performed).
- The [Droid CLI](https://docs.factory.com/droid-cli/overview.md) and a Factory
  account with access to your selected model for agent execution. Model calls
  may incur charges. Follow Factory's authentication instructions; never put
  credentials in `touchstone.toml`.

Building, running `version`, `config validate`, `demo --replay`, or `eval --replay`, and the default test suite do
**not** need Droid, an account, or live model calls. The adapter was probed against
Droid 1.4.1; see [verified capabilities and limits](docs/droid-capabilities.md).

## Try the demo

One command from a fresh checkout, with Go and Git but no credentials:

```sh
go run ./cmd/touchstone demo --replay
```

To run live with an authenticated Droid CLI, use `go run ./cmd/touchstone demo`.
Live calls use the default models and may incur charges. Both modes run the
real review pipeline on a bundled synthetic repository, never your current
checkout. Replay substitutes captured agent responses only and is labelled
as recorded. A complete demo exits 0 even though planted bugs are found.

The two specialists independently found the same two defects; all four claims
remain separate. The local check below validates whitespace only. These are
recorded model judgments, not a correctness guarantee; the security layer's
imprecise line numbers are preserved. See [demo details and reproduction
instructions](docs/demo.md). CI compares the command's full output with
[the checked-in sample](docs/demo-output.txt):

```text
Touchstone demo
RECORDED REPLAY: bundled agent responses, not live model judgment; no credentials needed.
Synthetic repository: ledger.go (empty-average panic and authorization bypass).
Local check: Git whitespace validation, not a correctness test.

Touchstone: findings
4 shown | 0 rejected | 0 filtered (min severity: info, confidence: 0)
  correctness: complete
  security: complete
  deterministic: complete
  verifier: complete
  Local check whitespace: complete | executed locally: true | argv: ["git","-c","core.whitespace=blank-at-eol,blank-at-eof,space-before-tab","diff","--check","--no-ext-diff","--no-textconv"]

  [ERROR] [f1] ledger.go:9 Division by zero for empty input
    confidence 0.99 | confirmed | layer: correctness
    Evidence: The guard `if len(cents) == 0 { return 0 }` was removed, so Average([]int{}) now reaches `return total / len(cents)` with len 0, panicking with a runtime division-by-zero. The d...
    Verifier: The diff removes the guard `if len(cents) == 0 { return 0 }`, so Average([]int{}) reaches `return total / len(cents)` with len 0, a Go integer division-by-zero panic. The doc co...

  [ERROR] [f2] ledger.go:14 Authorization check weakened from AND to OR
    confidence 0.99 | confirmed | layer: correctness
    Evidence: CanRead changed from `signedIn && user == owner` to `signedIn || user == owner`. Any signed-in user can now read a ledger they do not own (signedIn=true, user!="owner"), and any...
    Verifier: The diff changes CanRead from `return signedIn && user == owner` to `return signedIn || user == owner`. With signedIn=true and user!="owner" it now returns true (non-owner reads...

  [ERROR] [f3] ledger.go:17 CanRead grants access to any signed-in user regardless of ownership
    confidence 0.99 | confirmed | layer: security
    Evidence: The check changed from signedIn && user == owner to signedIn || user == owner, so any authenticated user (attacker-controlled 'user' not matching 'owner') passes, exposing ledge...
    Verifier: The diff's `signedIn || user == owner` lets any signed-in user (regardless of 'user' vs 'owner') pass the authorization check, a direct weakening from the prior `signedIn && use...

  [ERROR] [f4] ledger.go:11 Average divides by zero on empty slice
    confidence 0.98 | confirmed | layer: security
    Evidence: The len(cents) == 0 guard was removed, so Average([]) now computes total / len(cents) with len == 0, causing an integer division-by-zero panic in any caller passing an empty led...
    Verifier: Same removed guard at ledger.go: the deleted lines `if len(cents) == 0 { return 0 }` mean an empty slice reaches `total / len(cents)` with len == 0, causing an integer division-...
```

## Measure review quality

```sh
go build -o /tmp/touchstone ./cmd/touchstone
/tmp/touchstone eval --replay --baseline internal/eval/baseline.json
```

The 14-case public corpus includes five clean changes, subtle synthetic bugs,
and two reversed public CPython fixes. Three repetitions report precision,
recall, severity accuracy, clean-code noise, verifier impact, and incomplete
rate, with holdout scores and repeat spreads. Replay is explicitly recorded
and needs no credentials. Known invalid model outputs remain incomplete
(exit 2); baseline regressions exit 1. Live runs are manual and may incur charges.
See [eval format, matching, baseline, and external sets](docs/evals.md) and
[the measured baseline](docs/eval-baseline.md).

## Build and review

```sh
go build -o /tmp/touchstone ./cmd/touchstone
/tmp/touchstone version
/tmp/touchstone config validate --cwd .
# Model calls below require Droid authentication and may incur charges.
/tmp/touchstone review --cwd /tmp/synthetic-repo
/tmp/touchstone review --cwd /tmp/synthetic-repo --base main --head HEAD
/tmp/touchstone review --diff /tmp/synthetic.patch --cwd .
/tmp/touchstone review --diff - --concurrency 1 < /tmp/synthetic.patch
# Optional: specialist-only review; findings are explicitly unverified.
/tmp/touchstone review --diff /tmp/synthetic.patch --no-verify
# Explicit output and post-triage thresholds:
/tmp/touchstone review --diff /tmp/synthetic.patch --format markdown --min-severity warning --min-confidence 0.8
```

Without input flags, Touchstone compares HEAD with its merge-base against
`origin/HEAD`, falling back to local `main`, then `master`. Configure
`origin/HEAD` locally or use `--base REF` if that default cannot be found.
Explicit `--base REF` compares that commit directly to `--head REF` (default
HEAD), not its merge-base. Unknown/non-commit refs fail before model calls.
Only committed trees are diffed; staged, unstaged, and untracked changes are
not included. Git runs with argument vectors, external diff and textconv
disabled. There are no checkout or fetch operations.

Alternatively, `--diff FILE|-` reads a unified patch from a file or stdin.
It is mutually exclusive with both `--base` and `--head`. File paths resolve
relative to the shell's current directory. `--cwd` selects the repository
(from any subdirectory); review loads optional `touchstone.toml` at its root.
For `--diff` outside Git, config loads directly from `--cwd`.

Binary patches are excluded. `[input] ignore = ["generated/*", "*.bin"]` and
repeatable `--ignore GLOB` exclude matching files; CLI globs add to config.
Either side of a rename can match an ignore. Globs use the path syntax below.
Format-patch and mbox inputs are reviewed unchanged; binary and path exclusions
and per-path policies do not apply.
Empty or fully excluded diffs produce a clean report with no model calls or
local checks. Unresolvable patch paths fail rather than silently skipping files.

The default diff limit is 16 MiB. Set `--max-diff-bytes N` or
`[input] max_diff_bytes = N` (flag wins), from 1 through 268435456 bytes (256 MiB).
The raw diff is counted **before** exclusions; an oversized diff produces an
incomplete report with `diff too large, N bytes`, no truncated review and no
model/check calls. Capture is bounded while the remaining stream is counted.

The agent works in a private scratch
directory containing the supplied diff and, after the specialists finish, their
candidate findings, not in the configured repository.
It has no repository context beyond the diff. Synthetic examples only belong
in this public repository; do not commit private review inputs or outputs.

Both specialist layers run concurrently by default; `--concurrency N` bounds
active layers (positive integer). Each layer gets its own configured timeout,
starting after it acquires a worker slot. One fresh-session repair retry for
malformed model text shares that same timeout. Transport errors, process
failures, cancellation, and timeouts are not retried. A failed layer always
appears as `incomplete`, while successful peers' findings remain available.

Verification is on by default and starts only after both specialists finish.
It reads that same diff and a private candidate file in a new session, without
specialist conversations. It tries to disprove each claim and reject defects
not caused by the diff. Exactly one verdict per candidate is required; malformed
verdicts get one repair within the verifier's own timeout. A failed verifier
leaves **all** candidates unverified and the report incomplete. Explicitly
disabling it (`--no-verify` or config) leaves findings unverified without itself
making the report incomplete. No verifier call runs when there are no candidates.

Stdout defaults to readable terminal output on a TTY and JSON when piped.
Use `--format terminal|json|markdown` to select explicitly; Markdown is suitable
for copying into a PR comment, but Touchstone never posts it. Terminal color
requires a TTY and is disabled when `NO_COLOR` is present, even if empty.
See [examples of all three formats](docs/output-formats.md).

Triage keeps each claim and its own assessment. Similar cross-layer claims at
the same location receive possible-duplicate hints. Results rank by severity,
confidence, verifier verdict, and file/line, with stable tie-breakers.

`--min-severity info|warning|error` and `--min-confidence 0..1` filter **after**
triage. Defaults show everything; flags override `[output]` settings independently.
Every format reports the filtered count. JSON retains the filtered findings;
Markdown also includes a collapsed filtered audit section.
Exit status: **0** clean (including all retained findings filtered out), **1** findings,
**2** incomplete (even if some findings exist), **3** command/setup/output
error. Help/version/config validation also exit 0 on success. Diagnostics use
stderr, not stdout. See the stable [review-output contract](docs/review-output.md)
for downstream tooling and evals.

The default version is `dev`. For a local build label:

```sh
go build -ldflags '-X main.version=local' -o /tmp/touchstone ./cmd/touchstone
```

## Repository configuration

An optional `touchstone.toml` at the selected repository root overrides
built-in defaults. `config validate --cwd DIRECTORY` validates exactly that
directory, without root discovery. No user-wide Touchstone configuration is
loaded. An absent file uses defaults; an invalid
or unreadable file fails validation. Unknown keys and layer names are errors.

The layers are `correctness`, `security`, and `verification`. All
default to `glm-5.3-flash`, `low` reasoning, a five-minute timeout, autonomy
`off`, and read-only intent. These are configurable defaults, not a validated
model-quality recommendation. The verifier uses the same independent defaults
and overrides as the specialists; it does not inherit a specialist's overrides.

Set `model` per layer in `touchstone.toml`; there is no
`--model` CLI flag. Model and reasoning settings override
independently. The [GLM capability trial](docs/droid-capabilities.md#glm-53-flash-follow-up)
accepted 5/5 outputs as valid schema, but only 4/5 fully matched the requested
payload. Schema validity is not evidence of review quality or instruction
reliability.

For example, using only synthetic rules:

```toml
version = 1

[output]
min_severity = "warning"
min_confidence = 0.8

[layers.correctness]
model = "glm-5.3-flash"
reasoning_effort = "low"
timeout = "2m"
rules = ["Explain the observable failure for each proposed defect."]

[layers.verification]
enabled = true
model = "glm-5.3-flash"
reasoning_effort = "low"
timeout = "2m"
rules = ["Distinguish new defects from pre-existing behavior."]

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
`read_only`, `rules`, and `enabled` (default `true`). Both repository and path
overrides can disable or re-enable any model layer. `--no-verify`
always disables verification regardless of config. Repository policy must keep
`autonomy = "off"` and `read_only = true`; timeouts must be positive Go duration strings. Model
availability and model-specific reasoning support are checked by Droid at run
time, not by offline config validation.

The runner splits patches into files, resolves all three model layers for each
path, and groups files whose full resolved policies match. Groups run in diff
order, each with its own specialist/verifier sessions and private patch.
`--concurrency` limits parallel specialists within a group; groups run serially.
Rules are attached as **additional guidance**, never substitutes for the
built-in prompts or response contract. Renames use the destination for policy;
deletions use the deleted path. Local checks are repository-wide, not path rules.

Resolver precedence is defaults, repository layer settings, then matching path
rules.
Paths use Go's slash-separated [`path.Match`](https://pkg.go.dev/path#Match)
globs: `*` does not cross `/`; recursive `**` is rejected. Exact paths outrank
globs. Among globs, more characters other than `*?[]` mean higher precedence.
Equal specificity uses declaration order, with later scalar values winning.
Rules are additive in that order, never silently discarded. Relative,
normalized paths are required.

## Local deterministic checks

Checks execute code from the **current working tree**, even when `--head`
selects another commit. They do not independently prove the diff is correct.
They are off by default and never inferred from a package manager or file type.
Only explicitly configured commands can run, after `--run-checks` or
`[checks] enabled = true`. `--run-checks=false` overrides config.
**Treat the config as executable code:** inspect it before reviewing an
untrusted checkout. An untrusted config can enable checks; use
`--run-checks=false`, or remove that opt-in, for such checkouts.
This is not a sandbox. Commands can read/write files, access the network, and
launch other programs with your account's permissions. No shell is inserted,
but explicitly configuring a shell/interpreter is still possible and risky.

```toml
[checks]
enabled = false
env_allowlist = ["PATH", "HOME"] # inherit only these values; default: ["PATH"]

[[checks.commands]]
name = "synthetic tests"
args = ["go", "test", "./..."] # literal argv, no expansion or shell parsing
timeout = "30s"               # default 1m; maximum 30m
max_output_bytes = 65536      # default 64 KiB; maximum 1 MiB
location_regex = '(?m)^(?P<file>[^:\r\n]+):(?P<line>[0-9]+):'
```

Checks run serially at the Git root, once per review, after model work and
before triage. No command runs when no commands are configured.
At most 64 uniquely named commands may be configured. Executable lookup uses
the caller's PATH; use an absolute executable path when that matters.
The child environment contains only allowlisted inherited values (an empty
allowlist requests an empty environment, aside from platform-required values).
Never allowlist secrets or put them in arguments. Bounded combined stdout/stderr
is retained in JSON and failing-check evidence; avoid commands that print secrets.
Timeouts kill the process group on Unix, the direct child elsewhere.
Exceeding the combined output cap stops the command and marks it incomplete.

A nonzero normal exit becomes one `deterministic` error finding with confidence
1. Its confirmed verdict means **observed local failure**, not model agreement
or proof of a regression. No verifier model sees these findings.
Only a declared Go/RE2 `location_regex` enables location parsing. It must have
named `file` and `line` captures; the first match with a normalized relative
path and positive integer wins. Otherwise the finding is repository-level
(`file: ""`, `line: 0`). No output format is guessed.
Missing executables, timeouts, signals, capture errors, and output caps produce
`incomplete`, never a pass. Every format records the local status and literal
argv; JSON also retains exit status, bounded output and captured byte count.
Thresholds apply to these findings like other findings, but cannot hide an
incomplete outcome.

See the [worked synthetic repository example](docs/real-inputs.md), which also
shows a deterministic-only review without credentials.

## Agent and output contracts

The internal `Agent` interface runs one layer per invocation. The Droid adapter
uses prompt and settings files with private permissions, explicit model and
reasoning flags, a bounded subprocess deadline, and a fresh session every time.
It never resumes or forks a previous session. Unix deadlines kill the process
group; other platforms kill the direct child only.

The verifier uses reasoning and read-only file inspection only. It cannot run
code snippets, shell commands, tests, or other execution checks. Opt-in
deterministic checks are a separate local runner, not an expansion of the
model's tool permissions.

Runs omit `--auto`, restrict tools to `Read,Grep,Glob,LS`, disable hooks, and
disable cloud session sync in per-run settings. Autonomy and
tool restrictions are **not an OS sandbox**. Settings merge with local Droid
settings. Do not run this against untrusted repositories with host credentials.
Using Droid sends prompts and inspected content to the selected model service.

The CLI validates specialist responses and verifier verdicts against the
[review-output contract](docs/review-output.md). The foundation's
[legacy report contract](docs/findings.md) remains available to adapter callers.
A deterministic fake and synthetic recorded fixtures let tests run without an
account.

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

It runs both default specialist layers and, if needed, the verifier in fresh
sessions, with a three-minute overall deadline. It needs local Droid
authentication, sends the synthetic diff
to the model service, and may incur charges (including one repair per layer).
It checks transport and schema completion, not review accuracy. Normal tests
skip it; CI needs no model credentials.

A separate gated verifier smoke plants a real zero-division bug and a false
claim that the same diff introduced pre-existing integer truncation:

```sh
TOUCHSTONE_LIVE_VERIFIER_SMOKE=1 go test -v ./internal/review -run '^TestLiveDroidVerifierSmoke$' -count=1
```

It seeds specialist candidates deterministically, then invokes real headless
Droid only for verification. It must confirm the real bug and reject the false
positive, both with reasons. It uses the default verifier model/effort, the same
read-only adapter, and a three-minute overall deadline. It sends only synthetic
data and may incur charges, including one repair. This is one development smoke,
not an eval suite or a model-quality claim. Normal tests and CI skip it.

CI uses standard GitHub-hosted Ubuntu runners on this public repository, with
no model credentials, paid runner labels, or external review service. The only
runtime dependencies are `github.com/pelletier/go-toml/v2` for strict TOML
parsing and `golang.org/x/term` (with `x/sys`) for portable TTY detection.
CLI parsing, JSON validation, and
subprocess control use the Go standard library.

## License

Apache-2.0. See [LICENSE](LICENSE).

Copyright 2026 Dela Anthonio
