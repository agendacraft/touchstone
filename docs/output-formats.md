# Output formats

All examples are synthetic. Rendering never posts to a forge or runs checks.
Choose `--format terminal|json|markdown`; without it, a TTY gets terminal output
and a pipe/file gets JSON. Exit codes are the same for every format.

## Terminal

```sh
touchstone review --diff /tmp/synthetic.patch --format terminal --min-severity warning --min-confidence 0.7
```

Example excerpt from an incomplete review:

```text
Touchstone: incomplete
INCOMPLETE: a specialist layer or required verifier failed; this is not a clean review.
2 shown | 0 merged | 1 rejected | 1 filtered (min severity: warning, confidence: 0.70)
  correctness: complete
  security: incomplete (timeout)
  verifier: complete

src/calc.go
  [ERROR] [f1] src/calc.go:8 Synthetic zero-divisor
    confidence 0.95 | confirmed | layer: correctness
    Evidence: The synthetic change removes a required guard.
    Verifier: The diff supports this verdict.
```

Findings are grouped by file, with severity markers, confidence, verifier
verdict, source layer, and short evidence. Rejected findings appear
with their reasons below the main findings. Only a TTY gets color, and
`NO_COLOR` disables it even when set to an empty string:

```sh
NO_COLOR= touchstone review --diff /tmp/synthetic.patch --format terminal
```

A disabled or failed verifier adds a prominent `UNVERIFIED` banner. A failed
specialist or required verifier adds `INCOMPLETE`; even filtering every claim
cannot turn that result into a clean review.

## JSON

```sh
touchstone review --diff /tmp/synthetic.patch --format json > /tmp/review.json
```

For a completed review with no claims:

```json
{
  "schema_version": 2,
  "outcome": "clean",
  "findings": [],
  "layers": [
    {"name": "correctness", "status": "complete", "error": "", "attempts": []},
    {"name": "security", "status": "complete", "error": "", "attempts": []}
  ],
  "verifier": {"name": "verification", "status": "not_needed", "error": "", "attempts": []},
  "rejected": [],
  "triage": {
    "candidates": 0, "merged": 0, "shown": 0, "rejected": 0, "filtered": 0,
    "min_severity": "info", "min_confidence": 0
  }
}
```

Attempt arrays are abbreviated here; real invocations include usage, cost, and
duration. The [full JSON contract](review-output.md) includes a finding example.
Triage adds a unique `display_id` to each finding, optional
`possible_duplicate_of` references, a `triage` summary, and a `filtered`
array when nonempty. Each claim keeps its original assessment and reason.

## Markdown

```sh
touchstone review --diff /tmp/synthetic.patch --format markdown > /tmp/review.md
```

Example excerpt suitable for copying into a PR comment:

```markdown
## Touchstone review: incomplete

> **INCOMPLETE: a specialist layer or required verifier failed; this is not a clean review.**

| Shown | Merged | Rejected | Filtered |
| --- | --- | --- | --- |
| 2 | 0 | 1 | 1 |

Thresholds: severity ≥ warning; confidence ≥ 0.70.

| Layer | Status | Error |
| --- | --- | --- |
| correctness | complete |  |
| security | incomplete | timeout |
| verification | complete |  |

### ERROR

- **f1 — src/calc.go:8**: Synthetic zero-divisor
  - Severity: error; confidence: 0.95; verdict: confirmed; layer: correctness.
  - Evidence: The synthetic change removes a required guard.
  - Verifier: The diff supports this verdict.

<details>
<summary>Rejected findings (verifier reasons) (1)</summary>

- **f2 — src/calc.go:3**: Synthetic old-behavior
  - Severity: error; confidence: 0.70; verdict: rejected; layer: correctness.
  - Evidence: The synthetic change removes a required guard.
  - Verifier: This behavior existed before the synthetic diff.

</details>
```

Findings are grouped by severity. Rejected and filtered findings have collapsed
audit sections; verifier reasons remain visible when expanded. Possible duplicates are linked by their display IDs. Model text is escaped before it is
inserted into the comment.

The checked-in [renderer goldens](../internal/render/testdata) show complete
fixed-fixture output, including uncertain, rejected, filtered, incomplete,
unverified, and colored-terminal cases. No live model call produced these
fixtures; they test presentation, not review accuracy.
