# Review output contract, version 2

`touchstone review --diff FILE|- --format json` writes exactly one JSON report
on stdout once review execution completes. JSON is also the default when stdout
is not a TTY; terminal output is the interactive default. Version 2 adds independent verification, rejected
findings, verdict/rank metadata, and per-attempt latency to S1's version 1.
Consumers must check `schema_version`. The foundation's legacy adapter report
is unchanged. S3 extends version 2 additively with triage metadata, source
provenance, and filtered findings. Consumers must tolerate additional fields.
A clean report is not
proof that the diff is safe.

## Specialist response

Each fresh headless specialist session reads a private diff file by absolute
path and returns an array:

```json
[
  {
    "id": "synthetic-1",
    "severity": "error",
    "category": "logic",
    "file": "src/example.go",
    "line": 2,
    "title": "Zero input panics",
    "evidence": "The synthetic change removes the zero guard before 10/n.",
    "confidence": 0.95
  }
]
```

An empty result is `[]`. All eight fields are required, case-sensitive, and
non-null. Unknown fields, duplicate keys (including escaped aliases), coercion,
invalid UTF-8, non-JSON syntax, and extra JSON values are rejected.

- `id`: unique within one layer, 1–128 ASCII letters, digits, `.`, `_`, `-`,
  starting with a letter or digit.
- `severity`: `error`, `warning`, or `info`.
- `category`: non-whitespace string label, not a closed enum.
- `file`: normalized repository-relative slash path, no traversal, drive
  prefix, backslashes, NUL, or line breaks. A label, not authority to open a file.
- `line`: positive integer, new-file line (old-file location for deletions).
  Schema validation does not establish that the location exists in the diff.
- `title`, `evidence`: non-whitespace strings. Evidence should explain the
  observable failure, not just name a suspicious pattern.
- `confidence`: finite JSON number from 0 through 1. This is the model's
  self-assessment, not a calibrated probability.

The entire specialist model text is capped at 1 MiB, with at most 500 findings
per layer and JSON nesting depth 64. Limits reject the entire response, never
truncate or salvage a subset.

## Model-text framing and repair

Specialist and verifier responses must be exactly one JSON array, or exactly
one `json` or bare fenced code block containing exactly one array. Only JSON
whitespace (space, tab, CR, LF) may surround the array or entire fenced block.
Fence markers occupy their own lines. Opening lines contain only three
backticks, optionally followed immediately by lowercase `json`; closing lines
contain only three backticks. LF and CRLF line endings are accepted.

Prose before, after, or inside a fence, multiple arrays, multiple fences,
unsupported fence languages, and extra JSON values are rejected. For example,
`No findings: []` is malformed, never a clean result.

Malformed model text or schema violations get at most one fresh-session repair
attempt, with the original inputs/schema plus generic correction feedback.
Malformed output and parser diagnostics are not reinserted into the prompt.
Both attempts share one layer deadline. A broken transport envelope, process
failure, cancellation, or timeout fails immediately without repair. Failed
specialists contribute no findings, but never disappear from the report.

## Verifier response

After the specialists finish, a fresh independent session reads the **same diff
file** plus a private candidate JSON file, with no specialist conversation.
The embedded prompt asks it to disprove every finding, compare old/new behavior,
cite concrete diff evidence, and reject speculation or pre-existing behavior.

Only the foundation's read-only inspection tools are available. Code execution,
shell snippets, and network access remain unavailable; verification is reasoning
only. Widening that boundary requires a separate captain decision, not part of S2.

The candidate file includes `layer` and the eight specialist fields, with each
`id` qualified as `<layer>:<original-id>`, such as `correctness:synthetic-1`.
Original specialist IDs cannot contain `:`, so cross-layer collisions are
unambiguous. The public report retains original `(layer, id)` identities.

```json
[
  {
    "id": "correctness:synthetic-1",
    "verdict": "confirmed",
    "reason": "At line 2 the zero guard is removed; divide(0) now evaluates 10/0."
  }
]
```

All three fields are required, case-sensitive, non-null, and no others are
allowed. `id` must exactly match one candidate-file ID. `verdict` is `confirmed`,
`rejected`, or `uncertain`. `reason` is a non-whitespace string and should cite
concrete evidence; the parser checks structure, not whether reasoning is true.

Every candidate must receive exactly one verdict. Unknown, duplicate, or missing
IDs reject the **whole** response. Empty candidates need `[]`, but the runner
avoids that model call entirely. Limits are 1 MiB of model text, 1,000 verdicts
(two specialists of at most 500 each), and nesting depth 64. The same strict
framing, encoding, duplicate-key, type, and bounded repair rules above apply.
The verifier has its own configured timeout, beginning after specialist work.

## Merge rules

Only after the entire verifier response validates does the runner apply verdicts:

| Verdict | Main `findings` | `rejected` | Rank |
| --- | --- | --- | --- |
| `confirmed` | Kept | Not added | `normal` |
| `rejected` | Removed | Preserved with reason and original candidate fields | `normal` |
| `uncertain` | Kept and flagged with reason | Not added | `lower` |
| `unverified` | All candidates kept | Empty | `normal` |

`unverified` is a report-only state, never a valid model verdict. It has an empty
reason and is used when verification failed or was explicitly disabled.
Verifier failure makes the report incomplete. Explicit disable (`--no-verify`
or `[layers.verification] enabled = false`) does not itself make it incomplete.
No failed/malformed response can partially confirm or reject candidates.

An uncertain verdict is not a confirmed finding. Partial specialist failure stays
incomplete even when verification of available candidates succeeds or rejects
them all. Triage follows verification, as described below.

## Cross-layer triage and thresholds

Claims from different specialist layers merge when they have the same file and
line and their title token sets have Jaccard similarity of at least 0.5. Titles
are lowercased, punctuation separates tokens, and common stopwords are removed.
Every pair of claims in a group must match, and a group contains at most one
claim from each layer. Rejected claims are triaged separately.

The representative has the highest severity, then highest confidence, then
strongest verdict (`confirmed`, `unverified`, `uncertain`), then lowest layer
order. Its severity, confidence and verdict remain together. Evidence and
verifier reasons are combined in sorted, unique order. `rank` is `lower` for
an uncertain representative; rejected groups remain rejected.
Every original claim and its individual assessment remain in `sources`;
`layers` lists all contributing layers in sorted order.

Both retained and rejected groups sort by severity (`error`, `warning`, `info`),
descending confidence, verifier verdict (uncertain lower), file, and line.
Layer, ID, category, title, evidence, and reason break remaining ties.
Input order does not decide the representative or output order. Terminal output
groups by file and preserves this priority within each file.

`--min-severity` and `--min-confidence` apply only to retained groups **after**
merging. Bounds are inclusive; confidence must be finite and between 0 and 1.
Defaults are `info` and `0`. Optional `[output] min_severity` and
`min_confidence` in `touchstone.toml` set defaults, independently overridden by
explicit CLI flags (including confidence `0`). Rejected claims are not filtered.

`triage` contains:

| Field | Meaning |
| --- | --- |
| `candidates` | Original claims across retained and rejected sections |
| `merged` | Original claims combined into other groups |
| `shown` | Retained groups after filtering |
| `rejected` | Rejected groups after merging |
| `filtered` | Retained groups removed by either threshold, counted once |
| `min_severity`, `min_confidence` | Effective thresholds |

Thus `candidates = merged + shown + rejected + filtered`. A nonempty `filtered`
array preserves the excluded groups with the same shape as `findings`; it is
omitted when empty. Thresholds never hide the incomplete outcome.

## CLI report

```json
{
  "schema_version": 2,
  "outcome": "findings",
  "findings": [
    {
      "layer": "correctness",
      "id": "synthetic-1",
      "severity": "error",
      "category": "logic",
      "file": "src/example.go",
      "line": 2,
      "title": "Zero input panics",
      "evidence": "The synthetic change removes the zero guard before 10/n.",
      "confidence": 0.95,
      "verification": {
        "verdict": "confirmed",
        "reason": "The removed guard allows divide(0) to evaluate 10/0 at line 2."
      },
      "rank": "normal",
      "layers": ["correctness"],
      "sources": [
        {
          "layer": "correctness",
          "id": "synthetic-1",
          "severity": "error",
          "category": "logic",
          "file": "src/example.go",
          "line": 2,
          "title": "Zero input panics",
          "evidence": "The synthetic change removes the zero guard before 10/n.",
          "confidence": 0.95,
          "verification": {
            "verdict": "confirmed",
            "reason": "The removed guard allows divide(0) to evaluate 10/0 at line 2."
          }
        }
      ]
    }
  ],
  "layers": [
    {
      "name": "correctness",
      "status": "complete",
      "error": "",
      "attempts": [
        {
          "usage": {
            "input_tokens": 120,
            "output_tokens": 80,
            "cache_read_input_tokens": null,
            "cache_creation_input_tokens": null,
            "factory_credits": null,
            "thinking_tokens": null
          },
          "cost": null,
          "duration_ms": 800
        }
      ]
    },
    {
      "name": "security",
      "status": "complete",
      "error": "",
      "attempts": [
        {
          "usage": {
            "input_tokens": null,
            "output_tokens": null,
            "cache_read_input_tokens": null,
            "cache_creation_input_tokens": null,
            "factory_credits": null,
            "thinking_tokens": null
          },
          "cost": null,
          "duration_ms": 900
        }
      ]
    }
  ],
  "verifier": {
    "name": "verification",
    "status": "complete",
    "error": "",
    "attempts": [
      {
        "usage": {
          "input_tokens": 150,
          "output_tokens": 70,
          "cache_read_input_tokens": null,
          "cache_creation_input_tokens": null,
          "factory_credits": null,
          "thinking_tokens": null
        },
        "cost": null,
        "duration_ms": 600
      }
    ]
  },
  "rejected": [],
  "triage": {
    "candidates": 1,
    "merged": 0,
    "shown": 1,
    "rejected": 0,
    "filtered": 0,
    "min_severity": "info",
    "min_confidence": 0
  }
}
```

Existing version-2 keys remain emitted, including null unknown metadata. `findings`,
`rejected`, `layers`, and `attempts` are arrays, never null. `layers` always
contains `correctness`, then `security`, regardless of completion order.
Finding groups use triage order, not specialist/model order. IDs are layer-scoped:
use `(layer, id)`, not `id` alone; merged groups also expose the original
identities through `sources`. A report has at most 1,000 original claims across
retained, filtered, and rejected groups' sources. `rejected` uses the same
finding shape, with the rejected verdict and reason.

Specialist `status` is `complete` only after successful schema validation,
otherwise `incomplete`. `verifier` always exists, with the same result fields
and name `verification`. Its status is:

- `complete`: every candidate received a valid verdict, not necessarily confirmed.
- `incomplete`: required verification failed.
- `disabled`: explicitly disabled; no invocation.
- `not_needed`: no candidate findings were available; no invocation, including
  when specialists failed.

`disabled` and `not_needed` have empty error/attempts. Neither implies findings
were verified or specialists succeeded.

`error` is empty on completion or a safe machine-readable code:
`invalid_output`, `timeout`, `canceled`, `process_failure`, `invalid_request`,
`agent_error`, `prompt_error`, or `scratch_error` (verifier input-file setup).
It never embeds raw subprocess/model output. Attempts list every actual
invocation, in order, including failed attempts. Cancellation before an
invocation leaves attempts empty.

Usage is per attempt, including discarded malformed responses. Known values
are nonnegative integer counts; absent provider fields remain null, not zero.
`cost` is null when currency cost is unknown (currently always with Droid).
If an adapter supplies currency cost it has the form
`{"amount":0.01,"currency":"USD"}`. Factory credits are not converted to money.
Include all attempts when scoring cost/usage; missing values make a total
unknown, not free. No aggregate cost is fabricated.

`duration_ms` is measured monotonic wall time around each agent invocation,
including failed attempts, truncated to a nonnegative integer millisecond.
It excludes prompt/file preparation and schema parsing. Zero is possible for a
fast fake. Sum verifier attempt durations for its invocation latency, not the
parallel specialists' times. A skipped call fabricates neither usage nor cost.

## Outcome and exit status

| Outcome | Exit | Meaning |
| --- | --- | --- |
| `clean` | 0 | Specialists completed; no findings remain above thresholds (possibly all rejected or filtered). Enabled verification succeeded or was not needed. |
| `findings` | 1 | Specialists completed; at least one retained finding. Enabled verification succeeded; disabled verification leaves findings explicitly unverified. |
| `incomplete` | 2 | Any specialist or required verifier failed, even if findings exist or all available candidates were rejected. |
| No report | 3 | Invalid arguments/config/input, scratch setup failure before specialist execution, or output write failure. |

An output write failure can leave partial output in any format; exit 3 means consumers must not
use it as a complete report. Errors before report serialization leave stdout
empty. Help, version, and successful config validation exit 0 without a review
report. Diagnostics go to stderr. Missing/failed specialists or a failed required
verifier can never produce a clean outcome.

See [output format examples](output-formats.md) for terminal and Markdown output.
