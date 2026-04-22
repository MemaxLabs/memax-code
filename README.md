# Memax Code

Memax Code is the coding-agent CLI built on top of the Memax Go Agent SDK.

This repository is intentionally separate from the SDK. The SDK owns the
provider-neutral runtime and host-owned tool contracts; this CLI owns the
developer-facing product surface: flags, workspace wiring, event rendering,
session UX, and policy defaults.

## Status

Foundation. The first slice provides a runnable non-interactive CLI with:

- provider-neutral model profiles: `fast`, `balanced`, `deep`
- OpenAI and Anthropic provider adapters through the SDK
- root-confined workspace tools
- root-confined command execution tools
- managed command sessions for long-running processes
- JSONL-backed conversation sessions with resume, `latest`, activity listing,
  and transcript inspection
- dry-run configuration inspection
- local setup diagnostics with `memax-code doctor`
- event-stream rendering for assistant text, tool calls, command lifecycle,
  workspace edits, verification, usage, and final results, with `auto`, `live`,
  `tui`, and `plain` renderer modes

The CLI now has the first terminal UI foundation: `auto` chooses structured
terminal rendering for interactive output and plain rendering for logs, tests,
and pipes. `--ui live` opts into an early live status line while preserving the
sectioned transcript underneath. It does not yet ship the full-screen app shell,
session picker, slash commands, or sandboxed OS execution expected from a
mature coding-agent CLI. Those are product slices on top of this foundation.

## Usage

Inspect the resolved configuration without calling a model:

```sh
memax-code --dry-run --provider openai --profile deep --model gpt-5.4 "fix the failing tests"
```

Flags must precede the prompt because the CLI currently uses Go's standard
flag parser, which stops parsing flags at the first positional argument.

Run with OpenAI:

```sh
export OPENAI_API_KEY=...
memax-code --provider openai --model gpt-5.4 "inspect the workspace and suggest the next change"
```

Run with Anthropic:

```sh
export ANTHROPIC_API_KEY=...
memax-code --provider anthropic --model claude-sonnet-4-5 "repair the test failure"
```

Persist local defaults in `~/.memax-code/config.json`:

```sh
memax-code config init --provider openai --model gpt-5.4 --ui live
memax-code config show
```

```json
{
  "provider": "openai",
  "model": "gpt-5.4",
  "profile": "balanced",
  "effort": "auto",
  "ui": "live",
  "session_dir": "~/.memax-code/sessions",
  "inherit_command_env": false
}
```

Use a project-local config when needed:

```sh
memax-code --config .memax-code/config.json --dry-run "inspect this repository"
```

Configuration precedence is `flag > environment > config file > built-in
default`. The default config file is optional; an explicitly supplied
`--config` path must exist and decode as strict JSON.

Check local setup without calling a model:

```sh
memax-code doctor
memax-code doctor --config .memax-code/config.json --cwd .
```

`doctor` reports config loading, provider/model resolution, API-key presence,
session storage, workspace verification mode, and required local binaries. It
exits non-zero for usage errors, invalid config, or hard local setup failures.

Resume an earlier conversation:

```sh
memax-code --list-sessions
memax-code --show-session latest
memax-code --resume 0194d9a4-7b8c-7d20-9a1b-4f6c6f4f7a01 "continue from the last plan"
memax-code --resume latest "continue the most recent active session"
```

Session transcripts are stored under `~/.memax-code/sessions` by default. Use
`--session-dir` when you want project-local state, temporary test state, or a
different filesystem policy:

```sh
memax-code --session-dir .memax-code/sessions --list-sessions
```

Choose the event renderer explicitly when needed:

```sh
memax-code --ui live "repair the failing test"
memax-code --ui tui "inspect the failing test"
memax-code --ui plain "run the relevant checks" > run.log
```

`--ui auto` is the default. It uses the structured terminal renderer for
interactive terminals and the plain event stream for non-terminal writers, so
CI logs and redirected output remain stable.

`--ui live` is opt-in while the terminal UX is maturing. When output is
redirected, it falls back to the plain renderer so scripts never receive live
terminal control sequences. Its transient status line reports phase, elapsed
time, tool errors, active tool, command, approval, compact activity counts, and
usage while preserving the sectioned transcript underneath.
Operational events are rendered as a compact `[activity]` timeline so tool
calls, command lifecycle, approvals, workspace edits, verification, and errors
remain easy to scan without losing assistant text or final status.

`--list-sessions` prints sessions newest activity first, including the updated
time, created time, parent session, and the first user prompt as a short title.
Use `--show-session SESSION_ID` or `--show-session latest` to inspect the
readable transcript, including assistant text, tool calls, and tool results.

By default command tools do not inherit the host process environment. Enable it
only when the agent needs local toolchains that depend on environment variables:

```sh
memax-code --inherit-command-env "run the relevant tests and fix failures"
```

Configuration environment variables:

- `MEMAX_CODE_PROVIDER`: default provider, `openai` or `anthropic`.
- `MEMAX_CODE_CONFIG`: path to the JSON config file.
- `MEMAX_CODE_PROFILE`: default coding model profile.
- `MEMAX_CODE_EFFORT`: default reasoning effort, `auto`, `low`, `medium`,
  `high`, or `xhigh`.
- `MEMAX_CODE_PRESET`: default coding preset.
- `MEMAX_CODE_UI`: default renderer, `auto`, `live`, `tui`, or `plain`.
- `MEMAX_CODE_SESSION_DIR`: default JSONL session transcript directory.
- `MEMAX_CODE_INHERIT_COMMAND_ENV`: default command environment inheritance,
  accepting `1/0`, `t/f`, `true/false`, and case variants.
- `OPENAI_API_KEY`: OpenAI API key.
- `OPENAI_MODEL`: default OpenAI model when `--model` is omitted.
- `ANTHROPIC_API_KEY`: Anthropic API key.
- `ANTHROPIC_MODEL`: default Anthropic model when `--model` is omitted.

Relative paths in flags, environment variables, and config files resolve
against the process working directory at startup.

Verification is enabled automatically for Go workspaces with a root `go.mod`
and currently runs `go test ./...` or `go vet ./...` through the SDK verifier
tool. Non-Go workspaces disable required verification for now so the agent does
not get trapped behind an impossible Go verifier. Configurable verifier commands
are the next product slice.

## Development

The SDK dependency currently lives in the private `MemaxLabs` GitHub
namespace. Configure Go and git before dependency resolution:

```sh
gh auth setup-git
GOPRIVATE=github.com/MemaxLabs/* go test ./...
```

```sh
GOPRIVATE=github.com/MemaxLabs/* go test ./...
GOPRIVATE=github.com/MemaxLabs/* go run ./cmd/memax-code --dry-run "summarize this repository"
```
