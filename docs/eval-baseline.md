# Initial live evaluation, 2026-11-06

## Summary

The review produces useful claims, but the first measured result is not a
quality endorsement. Under the frozen location/category matching rules, it
found **17/27 expected bug opportunities (62.96%)**, with **27/57 retained
claims matching (47.37%)**. No clean-case claims were emitted, but two clean
reviews failed the response contract after repair. The verifier rejected one
matching bug claim and one unmatched claim.

All 42 original captures are checked in, including both failures. Cases,
aliases, ranges, severities, and model prompts were not tuned after seeing
results. There was no selective rerun or substitution.

## What changed

The eval corpus contains 14 cases, each run three times: nine buggy cases
(seven synthetic, two reversed public CPython fixes) and five clean cases.
Six cases are holdouts, reported separately. See [the eval guide](evals.md)
for matching rules, provenance, baseline policy, and limitations.

### Measured scores

These are micro-aggregates across case/repetition pairs. Every layer has 27
bug opportunities and 15 clean reviews. Repeated claims remain separate.

| Scope | Stage | Recall | Precision | Severity accuracy | False claims |
| --- | --- | ---: | ---: | ---: | ---: |
| Overall | Before verifier | 17/27 (62.96%) | 28/59 (47.46%) | 23/28 (82.14%) | 31 |
| Overall | After verifier | 17/27 (62.96%) | 27/57 (47.37%) | 23/27 (85.19%) | 30 |
| Correctness | Before verifier | 12/27 (44.44%) | 14/33 (42.42%) | 11/14 (78.57%) | 19 |
| Correctness | After verifier | 12/27 (44.44%) | 13/31 (41.94%) | 11/13 (84.62%) | 18 |
| Security | Before verifier | 14/27 (51.85%) | 14/26 (53.85%) | 12/14 (85.71%) | 12 |
| Security | After verifier | 14/27 (51.85%) | 14/26 (53.85%) | 12/14 (85.71%) | 12 |

- Clean noise: **0 flags / 15 clean reviews = 0 per clean case**, before
  and after verification. Only **13/15 clean reviews completed**; absence of
  findings on the other two is not evidence of a clean review.
- Reliability: **2/42 incomplete = 4.76%**. `10-clean-prefix-boundary`,
  repetition 1, failed security schema validation after two attempts.
  `08-clean-empty-guard`, repetition 3, failed correctness in the same way.
  Both responses added prose around the required array.
- Verifier: **2/59 rejected = 3.39%**. One unmatched claim was rejected
  correctly under the matcher; one matching bug claim was incorrectly
  rejected. Both were correctness claims. Security had zero rejections.
  Overall recall did not change, because another claim still matched the bug.
- Development, after verifier: recall **7/15 = 46.67%**, precision
  **10/30 = 33.33%**, severity accuracy **10/10 = 100%**.
- Holdout, after verifier: recall **10/12 = 83.33%**, precision
  **17/27 = 62.96%**, severity accuracy **13/17 = 76.47%**.
  Each partition had one incomplete review.

### Repeat spread

Each row summarizes the three complete-corpus repetition scores, not a
confidence interval. Layer, partition, and individual-case spreads are also
available in the command's JSON.

| Post-verifier metric | Minimum | Maximum | Mean |
| --- | ---: | ---: | ---: |
| Recall | 55.56% | 66.67% | 62.96% |
| Precision | 42.11% | 52.63% | 47.37% |
| Severity accuracy | 75.00% | 90.00% | 84.63% |
| False positives per clean case | 0 | 0 | 0 |
| Incomplete rate | 0% | 7.14% | 4.76% |

Severity's mean of three ratios differs from the micro-aggregate above.
Live review latency across 42 reviews was **11.450–90.392 seconds**, mean
**37.306 seconds**, excluding build/startup and scoring. Currency cost was
unavailable for all reviews, so it remains `null`, not zero. Raw reported
Factory credits per review were **341–2421**, mean **1160.38**, without any
currency conversion. Repairs are included.

### Important scoring limitation

Of the 30 retained unmatched claims, 27 missed the annotated location and
three missed the category. These are *scored* false positives, not a claim
that all 30 explanations describe imaginary defects. For example, the
map-zero-value case's first-run claims correctly describe missing-token
authentication but point at line 7, outside the frozen 5–6 range. The first
slice-alias claim points at line 6, outside its 4–5 range. The raw claims and
strict scores are both preserved rather than widening ranges afterward.

Conversely, a wrong explanation at a matching location/category can earn
credit. The verifier's correct/incorrect rejection labels use this same
proxy. This small public corpus, including known public vulnerabilities and
explicit API preconditions, does not establish general review performance.

## Why

A planted-bug demo cannot measure misses, clean-code noise, or unreliable
model responses. The recorded baseline makes those signals inspectable and
detects scoring, completion-state, input, or prompt drift without paid calls
in CI. It does not measure future live model quality.

## How it was tested

The manual run used the existing Droid agent, default `glm-5.3-flash` / `low`
for both specialists and verification, default two-specialist concurrency,
and the normal five-minute per-layer deadline with one schema repair.
The live executable's SHA-256 was:

```text
dc81c2e28d9e545c0ef08513eb02442051e5351b7907037d1736db059284376f
```

This executable was built during implementation on the demo slice. Final
eval bookkeeping was then completed without changing the review runner,
model prompts, response contract, or any case annotations. Replay through the
finished eval implementation verified identical fingerprints, findings,
rejections, original score fields, and all 42 recording files byte for byte.
The checked-in baseline is generated by that final replay, not by a second
live run.

```sh
touchstone eval --record /tmp/new-touchstone-recordings
# exit 2, all 42 reviews present; two invalid-output reviews retained

touchstone eval --replay --baseline internal/eval/baseline.json
# exit 2, baseline matched; known incomplete results are still incomplete
```

The CI comparison accepts this known exit 2 only after checking the exact
per-case layer completion/error states and every quality gate. New failures
or regressions exit 1, and recording/request drift or missing inputs exit 3.
No response was normalized into a valid model array.

Behavioral tests cover matching boundaries and aliases, duplicate recall
credit without merging, precision, severity, forbidden flags, verifier
mistakes, repeat/holdout aggregation, usage/cost, repair replay, cancellation,
baseline margins, completion drift, and CLI exit codes. Executable synthetic
oracles confirm that all seven original buggy-case inputs pass their API
contracts before the patch and fail after it; all five clean changes pass
both before and after. Both public regressions were independently reproduced
against pinned upstream source, then fixed again with their answer patches.

no user-visible change - screenshots not applicable
