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
- an interactive shell with slash commands, prompt history recall, and a
  dedicated terminal app surface for transcript, status, and composer state
- event-stream rendering for assistant text, tool calls, command lifecycle,
  workspace edits, verification, usage, and final results, with `auto`, `live`,
  `app`, `tui`, and `plain` renderer modes
- a machine-readable event stream with `--event-stream json` for wrappers,
  editors, and future GUI clients

The CLI now has the first real terminal UI foundation: `auto` chooses app mode
for interactive terminals and plain rendering for logs, tests, and pipes.
`--ui app` is still available when you want to be explicit, but it is now the
default human-facing terminal surface. The interactive app shell is a dedicated
terminal program with a transcript viewport, sidebar status, and a persistent
bottom composer instead of a prompt loop painted over ad hoc redraws. It runs
inline instead of taking over the alternate screen, so terminal scrollback
remains available while the shell is active.
`--ui live` keeps a lighter live status line while preserving the sectioned
transcript underneath.
The interactive shell keeps `/help`, `/session`, `/pick`, `/sessions`,
`/resume`, `/draft`, `/append`, `/show-draft`, `/submit`, `/cancel`,
`/history`, `/recall`, `/new`, and `/quit`. Submitted prompts are stored as
text-only JSONL history, separate from session transcripts, so recall works
across interactive shell restarts. The app shell is still Foundation quality:
it now has the right single-surface program structure, but the richer coding
agent timeline, approvals queue, and deeper composer behavior are still follow-on
product slices.

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
memax-code config init --provider openai --model gpt-5.4 --ui app
memax-code config show
```

```json
{
  "provider": "openai",
  "model": "gpt-5.4",
  "profile": "balanced",
  "effort": "auto",
  "ui": "app",
  "session_dir": "~/.memax-code/sessions",
  "history_file": "~/.memax-code/history.jsonl",
  "inherit_command_env": false,
  "verify_commands": {
    "test": "npm test",
    "lint": "npm run lint"
  }
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

Start an interactive shell:

```sh
memax-code
memax-code --interactive
memax-code --interactive --ui live
```

On a real terminal, running `memax-code` with no prompt opens the interactive
shell automatically. `--interactive` remains useful when you want that behavior
to be explicit in scripts, wrappers, or docs.

`--interactive --ui app` now runs as a dedicated terminal program. Transcript,
session state, slash-command output, and composer state all live on one surface.
The shell redraws inline rather than switching to an alternate screen buffer.

Inside the shell, type normal prompts to continue the current session. Slash
commands control local session state without calling a model:

```text
/help
/status
/pick
/show latest
/sessions
/resume latest
/resume 1
/draft Refactor this package
/append Preserve public API behavior
/show-draft
/submit
/history
/recall latest
/session
/new
/quit
```

Use `//` when a normal prompt needs to start with `/`, for example
`//etc/hosts is broken; investigate`. Inside an active draft, non-command lines
are accumulated until `/submit`; use `/cancel` to discard the draft. Slash
commands inside a draft must start at the beginning of the line, so indented
paths and code snippets such as `  /etc/hosts` stay in the draft.
Submitted prompts are remembered in `~/.memax-code/history.jsonl` by default;
use `/history` and `/recall N` to restore one into the draft before editing
and submitting again. Set `--history-file` when you want project-local,
temporary, or custom prompt recall storage. Multiple interactive shells can
append to the same JSONL file; each shell loads its recall view on startup and
does not live-refresh entries written by other shells. On Unix-like systems,
writes and compaction use an adjacent lock file. When the history grows past
625 parseable prompts, it is compacted to the most recent 500. Corrupt,
oversized, and very large new prompts are skipped for recall. Custom history
paths create a sibling `.lock` file; ignore both files when the path is inside
a project checkout.
In the terminal app shell, `Enter` sends the current prompt, `Ctrl+S` also
sends, `Ctrl+J` inserts a newline inside `/draft` mode, `PgUp/PgDn` and
`Home/End` scroll the transcript viewport, `F1` toggles help, and `Ctrl+C`
quits. The older line-oriented shell behavior remains on the non-app renderers.

Resume an earlier conversation:

```sh
memax-code --list-sessions
memax-code --show-session latest
memax-code --resume 0194d9a4-7b8c-7d20-9a1b-4f6c6f4f7a01
memax-code --resume 0194d9a4-7b8c-7d20-9a1b-4f6c6f4f7a01 "continue from the last plan"
memax-code --resume latest
memax-code --resume latest "continue the most recent active session"
```

When `--resume` is provided without a prompt on a real terminal, Memax Code
reopens that session in the interactive shell instead of requiring
`--interactive`. Non-terminal invocations still keep the normal missing-prompt
error path.

Session transcripts are stored under `~/.memax-code/sessions` by default.
Prompt recall history is stored separately under `~/.memax-code/history.jsonl`
so transcript retention and composer recall can be governed independently. Use
`--session-dir` and `--history-file` when you want project-local state,
temporary test state, or a different filesystem policy:

```sh
memax-code --session-dir .memax-code/sessions --list-sessions
memax-code --history-file .memax-code/history.jsonl --interactive
```

Choose the event renderer explicitly when needed:

```sh
memax-code --ui app "repair the failing test"
memax-code --ui live "repair the failing test"
memax-code --ui tui "inspect the failing test"
memax-code --ui plain "run the relevant checks" > run.log
```

For machine consumers, bypass the human renderer and stream structured events:

```sh
memax-code --event-stream json "repair the failing test"
```

`--event-stream json` writes one JSON object per line so wrappers can parse
incrementally while the run is still in progress. The user-facing mode name is
`json`; the transport stays line-delimited because a single JSON document is
not stream-friendly for long-running agent sessions.

`--ui auto` is the default. It uses app mode for interactive terminals and the
plain event stream for non-terminal writers, so CI logs and redirected output
remain stable.

`--ui app` is the default terminal mode while the terminal UX continues to
mature. When output is redirected, it falls back to the plain renderer so
scripts never receive terminal control sequences. The non-interactive app
renderer keeps the bounded transcript viewport and dashboard-style status for
single prompts. The interactive app shell now runs as a full Bubble Tea
program with a transcript viewport, sidebar, and persistent composer on one
surface while preserving terminal scrollback. This is still Foundation terminal
UX, not yet the full coding-agent timeline/composer product surface. Use
`--ui tui` when full session scrollback matters.
`--ui live` is the lighter-weight status line mode; it reports phase, elapsed
time, tool errors, active tool, command, approval, compact activity counts, and
usage while preserving the sectioned transcript underneath.
Operational events are rendered as a compact `[activity]` timeline so tool
calls, command lifecycle, approvals, workspace edits, verification, and errors
remain easy to scan without losing assistant text. The structured renderer ends
with a status panel that summarizes phase, session, counts, active tools, recent
command or patch context, approval state, usage, and errors.

`--list-sessions` prints sessions newest activity first, including the updated
time, created time, parent session, and the first user prompt as a short title.
Use `--show-session SESSION_ID` or `--show-session latest` to inspect the
readable transcript, including assistant text, tool calls, and tool results.

Configure project-specific verification commands when the workspace is not a
Go module, or when the default Go checks are not the right contract:

```sh
memax-code --verify-command 'test=npm test' \
  --verify-command 'lint=npm run lint' \
  "make the failing lint and test checks pass"
```

`--verify-command` accepts `name=command` and can be repeated. The names are
the checks the agent can request through `workspace_verify`, such as `test`,
`lint`, `typecheck`, or `default`. Empty/default verification requests use
`default` when it is configured, otherwise `test`. Commands run through the
same root-confined command runner as normal shell tools. For scoped checks,
include `{target}` in the configured command; the target must be one safe
package/path token and is passed as a single shell-quoted positional argument,
not expanded as shell syntax:

```sh
memax-code --verify-command 'test=npm test -- {target}' \
  "fix the tests for packages/api"
```

The same map can be stored in config:

```json
{
  "verify_commands": {
    "test": "npm test",
    "lint": "npm run lint",
    "typecheck": "npm run typecheck"
  }
}
```

In Go workspaces, custom commands extend the built-in Go verifier: if a custom
map does not define `test` or `vet`, those names still fall back to `go test
./...` and `go vet ./...`. In non-Go workspaces, only configured command names
are available.

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
- `MEMAX_CODE_UI`: default renderer, `auto`, `app`, `live`, `tui`, or `plain`.
- `MEMAX_CODE_SESSION_DIR`: default JSONL session transcript directory.
- `MEMAX_CODE_HISTORY_FILE`: default JSONL interactive prompt history file.
- `MEMAX_CODE_INHERIT_COMMAND_ENV`: default command environment inheritance,
  accepting `1/0`, `t/f`, `true/false`, and case variants.
- `MEMAX_CODE_VERIFY_COMMANDS`: JSON object mapping verification names to shell
  commands, for example `{"test":"npm test","lint":"npm run lint"}`.
- `OPENAI_API_KEY`: OpenAI API key.
- `OPENAI_MODEL`: default OpenAI model when `--model` is omitted.
- `ANTHROPIC_API_KEY`: Anthropic API key.
- `ANTHROPIC_MODEL`: default Anthropic model when `--model` is omitted.

Relative paths in flags, environment variables, and config files resolve
against the process working directory at startup.

Verification is enabled automatically for Go workspaces with a root `go.mod`
and runs `go test ./...` or `go vet ./...` through the SDK verifier tool unless
custom verification commands are configured. Non-Go workspaces disable required
verification unless `--verify-command`, config `verify_commands`, or
`MEMAX_CODE_VERIFY_COMMANDS` supplies host-owned checks.

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
