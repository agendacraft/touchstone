# Legacy adapter report contract, version 1

This foundation contract is retained for adapter callers that request a
complete report. `touchstone review` uses the distinct
[review-output contract](review-output.md), with runner-owned layer status and
strict specialist arrays, not this legacy model-authored envelope.

The adapter parses the Droid envelope first, then validates its `result` string
as exactly one JSON object. It does not extract Markdown fences, repair JSON,
or interpret prose. Successful schema validation says nothing about whether a
finding is true. Independent verification is not implemented.

```json
{
  "schema_version": 1,
  "outcome": "findings",
  "findings": [
    {
      "id": "synthetic-1",
      "layer": "correctness",
      "severity": "error",
      "file": "src/example.go",
      "line": 7,
      "title": "Invented boundary error",
      "description": "A synthetic input demonstrates the proposed failure.",
      "evidence": "An invented reproduction for a schema example only."
    }
  ]
}
```

All shown keys are required, case-sensitive, and non-null. Unknown fields and
duplicate JSON keys are rejected at every report level. The root version must
be integer `1`. `findings` must be an array, including for empty reports.

- `outcome`: `clean` requires no findings; `findings` requires at least one;
  `incomplete` allows zero or more findings and must not be treated as clean.
- `id`: unique within the report, 1–128 ASCII letters, digits, `.`, `_`, `-`,
  starting with a letter or digit.
- `layer`: `correctness` or `security`, identifying the proposed defect's
  specialty, not an assertion that a verifier ran.
- `severity`: `error`, `warning`, or `info`.
- `file`: normalized repository-relative slash path, without traversal, a
  drive prefix, backslashes, NUL, or line breaks. This is a location label,
  not permission to open a file or follow a symlink.
- `line`: positive integer.
- `title`, `description`, `evidence`: strings with non-whitespace content.

Reports are bounded to 1 MiB, 500 findings, and 64 nesting levels. Invalid
UTF-8, trailing JSON values, fractional line numbers, and type coercion are
rejected. These limits reject the whole report, never truncate findings.
Do not use model text as a shell command, trusted HTML, or a forge action.

The Droid transport envelope tolerates additional metadata keys for forward
compatibility, but requires a successful result envelope and validates known
usage fields. No raw subprocess output is included in user-facing run errors.
