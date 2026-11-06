# Review output contract, version 1

`touchstone review --diff FILE|-` writes exactly one JSON report on stdout once
review execution completes. This is the stable contract for consumers and the
future eval runner. It is distinct from the foundation's legacy adapter report.
All proposed findings are **unverified**. No deduplication, triage, or verifier
has run, and a clean report is not proof that the diff is safe.

## Specialist response

Each fresh headless agent session reads a private diff file by absolute path
and returns an array:

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

An empty result is `[]`. All eight shown fields are required, case-sensitive,
and non-null. Unknown fields, duplicate keys (including escaped aliases),
coercion, invalid UTF-8, non-JSON syntax, and extra JSON containers are rejected.

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

The entire model text is capped at 1 MiB, with at most 500 findings per layer
and JSON nesting depth 64. Limits reject the entire response, never truncate
or salvage a subset. The response must be exactly one JSON array, optionally
inside one bare or `json` code fence, with only whitespace outside the fence.
Prose, multiple fences, and extra JSON values are rejected.

Malformed model text or schema violations get at most one fresh-session repair
attempt, with the original diff and schema plus correction feedback. The
malformed text is not reinserted into the prompt. Both attempts share one layer
deadline. A broken transport envelope, process failure, cancellation, or timeout
fails the layer immediately without a repair. Failed layers contribute no
findings, but never disappear from the report.

## CLI report

```json
{
  "schema_version": 1,
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
      "confidence": 0.95
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
          "cost": null
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
          "cost": null
        }
      ]
    }
  ]
}
```

All keys are always emitted, including null unknown metadata. `findings`,
`layers`, and `attempts` are arrays, never null. `layers` always contains
`correctness`, then `security`, regardless of completion order. Findings are
concatenated in that layer order, preserving each model's array order. IDs are
scoped to the layer: use `(layer, id)` as the identity, not `id` alone. A review
can contain up to 1,000 findings. No duplicate is removed across layers.

`status` is `complete` only after successful schema validation, otherwise
`incomplete`. `error` is empty on completion or a safe machine-readable code:
`invalid_output`, `timeout`, `canceled`, `process_failure`, `invalid_request`,
`agent_error`, or `prompt_error`. It never embeds raw subprocess/model output.
Attempts list every actual invocation, in order, including failed attempts.
Cancellation before an invocation leaves `attempts` empty.

Usage is per attempt, including discarded malformed responses. Known values
are nonnegative integer counts; absent provider fields remain null, not zero.
`cost` is null when currency cost is unknown (currently always with Droid).
If a future adapter supplies currency cost it has the form
`{"amount":0.01,"currency":"USD"}`. Factory credits are not converted to money.
Consumers must include all attempts when scoring cost/usage; missing values
make a total unknown, not free. No aggregate cost is fabricated.

## Outcome and exit status

| Outcome | Exit | Meaning |
| --- | --- | --- |
| `clean` | 0 | Both layers completed and returned no findings. |
| `findings` | 1 | Both layers completed; at least one finding exists. |
| `incomplete` | 2 | At least one layer failed, even if another found defects. |
| No report | 3 | Invalid arguments/config/input, scratch setup failure, or output write failure. |

An output write failure can leave partial JSON; exit 3 means consumers must not
use it as a complete report. Errors before report serialization leave stdout
empty. Help, version, and successful config validation exit 0 without a review
report. Diagnostics go to stderr. A report never treats missing or failed
specialists as clean.
