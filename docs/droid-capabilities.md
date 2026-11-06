# Droid capability probe

Initial baseline verified on 2026-11-05 with Droid CLI 1.4.1, macOS arm64, model
`gpt-6.1-sol`, reasoning effort `low`. All inputs were invented for this
probe, in a temporary Git repository. No real review was performed.
The current default is `glm-5.3-flash` with `low` effort, covered by the
separate follow-up below. The baseline measurements are retained, not
attributed to the new model.

## Invocation

Each trial started a new process and session:

```text
droid exec -o json -f <temporary-prompt> --cwd <scratch-repository> \
  -m gpt-6.1-sol -r low --settings <temporary-settings> \
  --disable-builtin-skills
```

Settings disabled hooks and cloud session sync, and set the session autonomy
level to `off`. Settings are merged with the user's Droid settings, not an
isolated account or OS sandbox. No session continuation or fork flags were used.

## Read-only behavior

With no `--auto` flag, Droid read `sample.txt` and returned its exact synthetic
sentinel, exit 0 (9.73 seconds). A separate explicit attempt to edit that file
was denied, exit 1 (9.61 seconds). The sentinel was unchanged after every trial.
This is one read and one denied edit, not proof against every possible write.

`--auto read-only` is invalid: exit 1, empty stdout, and stderr
`Invalid --auto value. Allowed values: low, medium, high.`
Read-only means omitting `--auto`; even `--auto low` permits modifications.
Touchstone additionally limits read-only runs to `Read,Grep,Glob,LS` and disables
hooks. These are defense in depth, not an OS sandbox. Repository instructions,
local settings, credentials, and model-provider data handling remain trust
boundaries. Use disposable, sandboxed repositories for untrusted input.

## Results, failures, and deadlines

Successful stdout is a single object with `type: "result"`,
`subtype: "success"`, `is_error: false`, `duration_ms`, `num_turns`,
`result` (a **string**, not a report object), `session_id`, and `usage`.
The permission failure had the same envelope, `subtype: "failure"`,
`is_error: true`, and a free-text explanation in `result`; stderr was empty.
Preflight failures need not emit any JSON.

Observed usage keys: `input_tokens`, `output_tokens`,
`cache_read_input_tokens`, `cache_creation_input_tokens`, `factory_credits`,
`thinking_tokens`, and `ttft_ms`. No currency cost was emitted. Credits are not
dollars; Touchstone leaves currency cost unknown rather than inventing a price.
Fixtures retain these shapes and synthetic content, with session IDs replaced.

An external one-second deadline killed a separate run with SIGKILL (Python
return code -9), after 1.02 seconds, with **empty stdout and stderr**. This was
an enforced subprocess deadline, not a native Droid timeout response or exit
code. The adapter must enforce its own deadline and must not require JSON on
failure. This trial did not measure a provider-side request timeout.

## Tiny schema trial (initial baseline)

Three independent prompts requested exact JSON keys and types, no Markdown:
one clean report, one invented correctness finding, and one incomplete report.
All **3/3** returned parseable JSON in the `result` string matching the requested
schema. All **3/3** had the requested outcome and finding count; the single
finding had the requested file, line, ID, and required nonempty fields.
Wall times were 6.37, 6.40, and 11.57 seconds. This convenience sample is too
small to establish reliability, precision, review quality, or a production
success rate. No retry or repair was needed, and none is implemented.

The application validates every report independently. Plain text, Markdown
fences, missing or unknown keys, duplicate keys, invalid locations and types,
and contradictory outcomes are errors, not findings or a clean result.

## GLM-5.3-Flash follow-up

Verified on 2026-11-05 with the same Droid 1.4.1 installation and a new
synthetic scratch repository. Every attempt used a fresh session, `-o json`,
`-f`, `--cwd`, `-m glm-5.3-flash`, explicit `-r`, and per-run settings with
hooks and cloud sync disabled and autonomy `off`. Tool access was restricted
to `Read,Grep,Glob,LS`. Each invocation had a 60-second external deadline.
No attempt timed out or was retried.

### Reasoning effort arguments

A trivial prompt requested exactly `OK` at each of the nine effort values
accepted by Touchstone's offline config validator:

| Effort argument | Exit | Exact `OK` | Wall seconds |
| --- | --- | --- | --- |
| `none` | 0 | yes | 10.34 |
| `off` | 0 | yes | 13.62 |
| `minimal` | 0 | yes | 13.77 |
| `low` | 0 | yes | 13.90 |
| `medium` | 0 | yes | 16.88 |
| `high` | 0 | yes | 25.83 |
| `xhigh` | 0 | yes | 10.46 |
| `max` | 0 | yes | 12.75 |
| `dynamic` | 0 | yes | 13.08 |

The CLI accepted **9/9** arguments and emitted successful JSON envelopes.
However, `droid exec --help` advertises only `low`, `high`, and `max` for this
model (native default `high`). The result envelope does not expose the
effective effort, so acceptance does **not** establish that the other six
values are honored rather than normalized. Touchstone defaults to `low`,
which is both advertised and verified to execute. This is not a measured
review-quality or performance recommendation.

### Five strict-schema trials at low effort

Five independent prompts used the same version-1 contract as the baseline.
All returned exit 0. Raw envelopes were passed through the application's
actual Droid-envelope and findings parsers, without repair or extraction:

| Trial | Requested payload | Schema valid | Full request matched | Wall seconds |
| --- | --- | --- | --- | --- |
| 1 | clean, zero findings | yes | yes | 13.98 |
| 2 | one correctness **error**, ID `demo-1`, line 7 | yes | **no** | 10.79 |
| 3 | incomplete, zero findings | yes | yes | 10.23 |
| 4 | one security warning, ID `demo-2`, line 11 | yes | yes | 11.44 |
| 5 | clean, zero findings | yes | yes | 8.71 |

Strict-schema acceptance was **5/5 (100%)**. Full requested-payload adherence
was **4/5 (80%)**: trial 2 returned severity `warning` instead of the requested
`error`. Both are valid schema enum values, so the parser correctly accepted
the report; it cannot infer whether the model followed the request or whether
an invented defect is true. This miss is retained in the results, not retried
or hidden. The sample is too small to establish reliability, review quality,
or production success rates. Independent verification remains follow-up work.

Public references: [headless execution](https://docs.factory.com/droid-exec/overview.md)
and [runtime settings](https://docs.factory.com/droid-cli/settings.md).
