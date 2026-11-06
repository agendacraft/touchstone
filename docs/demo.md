# Bundled demo

From a fresh checkout, with Go 1.25+ and Git:

```sh
go run ./cmd/touchstone demo --replay
```

Replay needs no Droid installation, account, credentials, or model calls.
The initial Go build may download the module dependencies.
The executable also works outside this checkout because its fixtures are embedded:

```sh
go build -o /tmp/touchstone ./cmd/touchstone
/tmp/touchstone demo --replay
# Live model calls require Droid authentication and may incur charges.
/tmp/touchstone demo
```

## What runs

Every invocation creates a private temporary Git repository, stages the bundled
`ledger.go` baseline, and applies `buggy.patch`. No commits, Git identity changes,
fetches, or edits to your current repository occur. The demo uses its bundled
`touchstone.toml`, not the current directory's config. Temporary files are
removed on return, including ordinary errors and cancellation.

The baseline promises an empty average of zero and access only for signed-in
owners. The patch removes the empty-input guard and changes the access check
from `&&` to `||`. Both defects are verified independently by executable fixture
tests. No private repository content is included.

The command calls the same review entry point as `touchstone review --diff`:

1. Load repository config and read/filter the bundled diff.
2. Run independent correctness and security specialists.
3. Parse their responses and send layer-qualified candidates to a fresh verifier.
4. Execute the local `git diff --check` whitespace check against the patched tree.
5. Triage and render the layered report without merging or deduplicating claims.

In replay, only the Agent boundary changes: distinct synthetic model names
select the saved responses, safely independent of concurrent call order.
Parsing, verifier coverage validation, local execution, triage, and rendering
remain real. The terminal report is deliberately plain text even when piped
or viewed on a color-capable terminal.

The local check only validates whitespace, not behavior or security. The report
labels it as executed locally, separate from model verification.

## Recording and limits

The three response files in `internal/demo/fixtures/`, `correctness.txt`,
`security.txt`, and `verification.txt`, contain model text captured from a successful
live demo on 2026-11-06, using `glm-5.3-flash` with `low` reasoning and the
existing Droid adapter. Code fences and the original finding fields are retained;
transport envelopes, session identifiers, usage, and costs are not replayed.

Both specialists reported both planted defects, so four independent claims
remain. This particular verifier confirmed all four. Its agreement is model
judgment, not proof. The security specialist reported old-file line numbers
11 and 17 instead of new-file locations 9 and 14; those recording inaccuracies
are intentionally preserved, not silently repaired. Live runs can differ in
claims, locations, verdicts, wording, time, and cost. This is a demonstration,
not an accuracy benchmark or an eval suite.

A missing Droid executable fails with instructions to use `--replay`. Agent
transport or authentication failures produce an incomplete review rather than
falling back to recorded output. Missing Git is a setup error in either mode.
The demo exits **0** for a complete review, including the expected planted
findings, **2** for an incomplete review, and **3** for command/setup/output
errors. The ordinary `review` exit contract is unchanged.

## Reproduce the checked-in output

```sh
go build -o /tmp/touchstone ./cmd/touchstone
/tmp/touchstone demo --replay > /tmp/touchstone-demo-output.txt
diff -u docs/demo-output.txt /tmp/touchstone-demo-output.txt
```

To intentionally refresh the presentation snapshot after reviewing a change:

```sh
go run ./cmd/touchstone demo --replay > docs/demo-output.txt
```

Copy that exact output into the README's demo text block too. Tests check both
public snapshots; the dedicated CI demo job builds the command, runs replay
without credentials, and fails on any output or exit-status drift.
This regeneration replays the saved model text, it does not recapture models.
