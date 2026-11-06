# Worked example: a synthetic repository

This example uses only local, synthetic files. It does not post, fetch, run an
eval, or require model credentials. Git commits use your existing Git identity;
configure that yourself if it is not already available.

Build Touchstone from its checkout:

```sh
go build -o /tmp/touchstone ./cmd/touchstone
```

Create an isolated sample with a passing test and deliberately disable all
model layers, so the first review exercises only the opt-in local runner:

```sh
sample=$(mktemp -d)
git -C "$sample" init --initial-branch=main
printf 'module example.invalid/synthetic\n\ngo 1.25.0\n' > "$sample/go.mod"
cat > "$sample/divide.go" <<'GO'
package synthetic

func Divide(n int) int {
    if n == 0 { return 0 }
    return 12 / n
}
GO
cat > "$sample/divide_test.go" <<'GO'
package synthetic

import "testing"

func TestZero(t *testing.T) {
    if got := Divide(0); got != 0 { t.Fatalf("got %d, want 0", got) }
}
GO
cat > "$sample/touchstone.toml" <<'TOML'
version = 1

[layers.correctness]
enabled = false
[layers.security]
enabled = false
[layers.verification]
enabled = false

[[paths]]
pattern = "divide.go"
[paths.layers.correctness]
rules = ["Check the documented zero-input behavior."]

[checks]
enabled = false
env_allowlist = ["PATH", "HOME", "GOCACHE", "GOPATH", "TMPDIR"]
[[checks.commands]]
name = "synthetic unit tests"
args = ["go", "test", "./..."]
timeout = "30s"
max_output_bytes = 65536
TOML
git -C "$sample" add .
git -C "$sample" commit -m "Add synthetic guarded division"
git -C "$sample" switch -c feature
```

Introduce a zero-input panic and commit it:

```sh
cat > "$sample/divide.go" <<'GO'
package synthetic

func Divide(n int) int { return 12 / n }
GO
git -C "$sample" add divide.go
git -C "$sample" commit -m "Remove synthetic guard"
```

Review the two committed trees:

```sh
# No model or check runs: all model layers and local checks are disabled.
/tmp/touchstone review --cwd "$sample" --base main --format terminal
# Exit 0, clean, with disabled statuses. This is NOT evidence of safety.

# Explicitly trust and run the configured local test command.
/tmp/touchstone review --cwd "$sample" --base main --run-checks --format terminal
# Exit 1, findings: one repository-level deterministic error.
# The output states that ["go","test","./..."] ran locally.

# With no input flags: merge-base(main, HEAD), same committed change.
/tmp/touchstone review --cwd "$sample" --run-checks --format json
# Exit 1, with the command's bounded output in checks.

# Empty diff: exit 0 and no model/check calls, even with --run-checks.
/tmp/touchstone review --cwd "$sample" --base HEAD --run-checks

# A deliberately tiny cap: exit 2, "diff too large, N bytes".
/tmp/touchstone review --cwd "$sample" --base main --max-diff-bytes 8

# Existing file/stdin input works too.
git -C "$sample" diff main HEAD -- > "$sample/synthetic.patch"
/tmp/touchstone review --cwd "$sample" --diff "$sample/synthetic.patch" --run-checks
```

To include actual specialist review, enable `correctness`, `security`, and
`verification` in this synthetic config, then rerun after Droid authentication.
The path's extra guidance augments the built-in correctness prompt; it never
replaces the schema or the read-only model boundary. Those calls may incur
charges. The checks still execute the current checkout, not an alternate
`--head` tree.

## Policy conflicts

For a hypothetical `src/example.go`, policy resolves as follows:

| Policy | Enabled | Extra rules |
| --- | --- | --- |
| Built-in correctness | true | none |
| Repository correctness override | false | repository guidance |
| Matching `src/*.go` | true | broad guidance |
| Matching exact `src/example.go` | false | exact guidance |
| Later matching exact `src/example.go` | true | final guidance |

The final layer is enabled and receives all four configured guidance entries
in precedence order. Exact paths outrank globs even if declared earlier;
equal specificity uses later scalar values. This applies independently to each
of the three model layers. Ignore globs exclude whole patches before grouping;
path rules cannot re-enable an ignored file.

PR input (`--pr`) is deliberately deferred to keep this slice focused on
offline, local inputs and checks.
