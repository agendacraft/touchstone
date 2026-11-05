# Droid capability probe

Verified on 2026-11-05 with Droid CLI 1.4.1, macOS arm64, model
`gpt-6.1-sol`, reasoning effort `low`. All inputs were invented for this
probe, in a temporary Git repository. No real review was performed.

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

## Tiny schema trial

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

Public references: [headless execution](https://docs.factory.com/droid-exec/overview.md)
and [runtime settings](https://docs.factory.com/droid-cli/settings.md).
