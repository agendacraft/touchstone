# Review evals

The demo shows planted defects. Evals also measure misses, noisy claims on
clean changes, severity agreement, verification impact, and model reliability.
This is a small regression corpus, not evidence of production review quality.

```sh
# No account or Droid needed. JSON goes to stdout, progress to stderr.
go run ./cmd/touchstone eval --replay

# Compare with the checked-in baseline. Exit 2 is expected if the baseline
# contains the same incomplete model responses. Exit 1 is a regression.
go run ./cmd/touchstone eval --replay --baseline internal/eval/baseline.json

# Manual live run: requires authenticated Droid, sends the public case diffs
# to the model service, may incur charges. Three independent runs per case.
go run ./cmd/touchstone eval --record /tmp/new-touchstone-recordings
```

For reliable exit codes in scripts, build the executable first. `go run`
wraps nonzero program exits. A complete evaluation exits **0**, even when it
misses bugs. A baseline regression or completion drift exits **1**.
Incomplete review exits **2**, including a matching known-incomplete baseline.
Invalid inputs, recording/request drift, or output errors exit **3**.

## Cases

Each immediate subdirectory is a case, processed in lexical order. It contains:

- `diff.patch`: a nonempty unified text diff, at most 16 MiB. Include the
  context the reviewer needs in this diff. No repository checkout or network
  fetch occurs. Binary patches are unsupported.
- `expected.json`: the ground truth and provenance, version 1.
- `recording-1.json`, `recording-2.json`, `recording-3.json`: captured raw agent
  turns for credential-free replay, including repairs and failures.
- `fix.patch` for public reversed-fix cases: the upstream answer, never passed
  to the reviewer.

For example, this annotation represents one required defect and one nearby
behavior the review must not flag:

```json
{
  "version": 1,
  "difficulty": "medium",
  "holdout": false,
  "source": "Original synthetic case, Apache-2.0, Touchstone contributors (2026).",
  "rationale": "An invalid index equal to the slice length must return false.",
  "findings": [
    {
      "file": "sample.go",
      "start": 4,
      "end": 7,
      "category": "bounds",
      "aliases": ["logic", "off-by-one"],
      "kind": "must-find",
      "severity": "error"
    },
    {
      "file": "sample.go",
      "start": 10,
      "end": 12,
      "category": "arithmetic",
      "kind": "must-not-flag"
    }
  ]
}
```

Paths must be normalized repository-relative paths appearing in the patch.
Ranges are positive, inclusive new-file line numbers (old locations for
deleted lines, as in the review contract). Categories are explicit labels;
aliases accommodate known vocabulary differences, not arbitrary similarity.
Overlapping ranges with intersecting categories are rejected as ambiguous.
Every required defect needs severity `error`, `warning`, or `info`.
An empty `findings` array is a clean case. Difficulty is `easy`, `medium`, or
`hard`. Source, rationale, version, and a findings array are required.

The bundled corpus has **14 cases: nine buggy and five clean**, across all
three difficulty levels. Seven defects are synthetic, including slice aliasing,
map zero values, expiry boundaries, and integer overflow. Five clean changes
exercise equivalence, explicit preconditions, and existing behavior.
Two cases reverse real public CPython fixes:

- IPv4 leading-zero rejection:
  [60ce8f0be6354ad565393ab449d8de5d713f35bc](https://github.com/python/cpython/commit/60ce8f0be6354ad565393ab449d8de5d713f35bc).
- URL C0-control stripping:
  [2f630e1ce18ad2e07428296532a68b11dc66ad10](https://github.com/python/cpython/commit/2f630e1ce18ad2e07428296532a68b11dc66ad10).

Their annotations record authors, changes, and attribution. These excerpts
remain under the included
[Python licence](../internal/eval/cases/PYTHON-LICENSE.txt), not Touchstone's
Apache licence. Only relevant code hunks are reversed; the URL case omits the
documentation-only hunk and adjusts hunk positions. Public answers and licence
text are included in the binary's bundled assets.

Six cases are marked `holdout`, including two clean changes and both public
fixes. Their results are reported separately from the eight development cases.
Holdout means excluded from prompt/case tuning, not secret or unknown to a
model's training data. Freeze cases, category aliases, severity, and ranges
before running models. Do not revise them to improve a measured score.

## Pipeline and replay

Evals invoke the existing `review.Runner`, using the normal default
correctness and security policies, fresh specialist sessions, the independent
verifier, and ordinary triage with no display filtering. They do not change
the model-response contract, merge claims, or deduplicate findings.
Only the agent boundary is substituted in replay.

The pipeline sees only the diff and its own generated candidates, in its normal
private scratch directory. Ground truth, case names, difficulty, holdout labels,
and answer patches are not given to the agent. No eval-specific repository
config or deterministic commands are executed.

Live mode defaults to three repetitions (`--repeats 1..20`). Every repetition
uses new sessions, including the normal bounded schema-repair attempt.
`--record` requires a new directory so it cannot overwrite existing evidence.
Each recording captures the original model policy, raw responses, usage,
reported currency cost, and transport-error kind, without session identifiers.
Schema failures are preserved, not rewritten as clean JSON.

Replay is labelled **recorded**, in both JSON (`"mode": "recorded"`) and
stderr. It requires exactly the requested repetitions, substitutes no live
calls, and fails on missing or unused turns. SHA-256 binds each recording to
the exact diff and annotation bytes, normalized emitted prompts, and candidate
file bytes. Changes to prompts or verifier inputs require new captures, not
silently reused verdicts. Replay uses the *captured* policy; it cannot tell
whether a newly selected live model is better.

## Matching and scores

This is deterministic **location/category matching**, not an LLM judge:

1. A claim matches a `must-find` annotation only when the file is identical,
   its line falls in the inclusive range, and its category equals the
   annotation or an explicit alias after trimming and lowercasing.
2. All unmatched claims are false positives, including claims outside annotated
   regions. Every claim on a clean case is a false positive.
   `must_not_flags` additionally counts explicit forbidden-region/category hits.
3. Every claim remains separate. Multiple claims about one bug each count in
   precision; they find that expected bug only once for recall within a
   case/repetition/scope. Overall recall credits either specialist, not the sum
   of their duplicate hits. Each specialist is evaluated against all bugs.
4. `before_verifier` includes retained, filtered, and rejected claims.
   `after_verifier` includes retained and filtered claims, including uncertain
   and unverified claims. Display filtering cannot hide noise.

Metrics are micro-aggregated from counts across case/repetition pairs, for each
specialist and overall:

- **Recall:** expected bugs hit / expected bugs.
- **Precision:** matching claims / all claims.
- **Severity accuracy:** matching claims with exactly the expected severity /
  matching claims. Severity errors do not also become false positives.
- **False positives per clean case:** clean-case false claims / clean reviews.
- **Verifier rejection rate:** rejected claims / all pre-verifier claims.
  Rejected matching claims count as incorrectly rejected; rejected unmatched
  claims count as correctly rejected. These counts are also split by source layer.
- **Incomplete rate:** incomplete reviews / all case/repetition pairs. An
  invalid model response is a reliability failure even on clean code. Partial
  findings remain scored; incomplete reviews are never dropped.

An undefined ratio is JSON `null`, not an invented perfect score. Counts remain
available. The `spread` map reports minimum, maximum, mean, and number of defined
samples, across repeat-level metrics for overall/development/holdout and across
repeats for each `case/<name>` metric.

Currency cost is summed per review only if every attempt has a reported cost in
the same currency. Otherwise it is `null`; credits are not converted to money.
Raw Factory credits are also summed per review when all attempts report them,
with a `factory_credits_per_review` spread in the same provider units. Tokens
and per-attempt credits remain in the raw reports. Live `latency_ms` measures review
wall time, including parallel specialists, verification, repairs, and scratch
setup, but excluding build/startup, scoring, and output. Its spread is across
individual reviews. Replay `latency_ms` is `null`; raw report attempt durations
measure local playback, **not** model latency. Captured cost remains recorded
cost, not a new charge.

Strict location/category matching can count a real but mislocated or differently
labelled bug as a miss and its claim as noise. A wrong explanation at the right
location/category can receive credit. Verifier impact shares those limitations.
Inspect the preserved claims alongside the numbers; neither high scores nor
agreement among models proves correctness.

## Baseline and CI

`internal/eval/baseline.json` is a recorded baseline. The optional CI job runs
by default; set repository variable `TOUCHSTONE_EVALS=false` to disable it.
It has no model credentials and never runs live agents.

Comparison checks every case/repetition, both before and after verification,
for each layer and overall. It rejects drops in recall, precision, or severity
accuracy, increases in false positives or forbidden hits, and more incorrectly
rejected bug claims beyond the configured margin. It also rejects case,
annotation, repetition, or exact layer completion/error-state drift.
Known incompleteness is preserved, not a green model verdict.

The baseline's default margin is zero. `--margin 0.02` overrides it with two
percentage points for ratios and 0.02 claims for count gates. Counts are
integers, so a fractional count margin permits no additional false claim.
Latency and cost are not baseline gates. Undefined ratios are not compared;
recall and count gates still apply. Improvements in quality metrics are allowed.
Any completion-state change, including a fixed schema failure, requires an
explicit baseline refresh.

The CLI compares the baseline **before** returning incomplete exit 2. A
regression takes precedence and returns 1. CI accepts 0 or 2 only from that
baseline comparison command; a missing baseline or recording fails. This means
a known invalid output cannot excuse a new failure elsewhere.

To deliberately replace the baseline after reviewing new public recordings:

```sh
touchstone eval --replay --recordings /tmp/new-touchstone-recordings \
  --write-baseline /tmp/proposed-baseline.json --margin 0
```

Review the score and completion changes before copying the proposed file into
the repository. Do not refresh a baseline merely to make a regression pass.
See [the initial measured run](eval-baseline.md) for scores and limitations.

## External case sets

`--cases /absolute/external/path` selects an external set instead of bundled
cases. `--recordings /absolute/external/recordings` selects separate replay
captures. No automatic discovery, upload, or import occurs.

Keep private company-derived cases and their responses **outside this public
repository**, including baselines and reports. Do not put them under bundled
assets, docs, or testdata. Live runs send their diffs to the selected provider;
obtain permission before doing that. Only synthetic and licence-compatible
public material belongs in the checked-in corpus.
