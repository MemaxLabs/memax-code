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
- JSONL-backed conversation sessions with resume, `latest`, and activity listing
- dry-run configuration inspection
- event-stream rendering for assistant text, tool calls, command lifecycle,
  workspace edits, verification, usage, and final results

The CLI does not yet ship the full-screen TUI, session picker, slash commands,
or sandboxed OS execution expected from a mature coding-agent CLI. Those are
product slices on top of this foundation.

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

Resume an earlier conversation:

```sh
memax-code --list-sessions
memax-code --resume 0194d9a4-7b8c-7d20-9a1b-4f6c6f4f7a01 "continue from the last plan"
memax-code --resume latest "continue the most recent active session"
```

Session transcripts are stored under `~/.memax-code/sessions` by default. Use
`--session-dir` when you want project-local state, temporary test state, or a
different filesystem policy:

```sh
memax-code --session-dir .memax-code/sessions --list-sessions
```

`--list-sessions` prints sessions newest activity first, including the updated
time, created time, parent session, and the first user prompt as a short title.

By default command tools do not inherit the host process environment. Enable it
only when the agent needs local toolchains that depend on environment variables:

```sh
memax-code --inherit-command-env "run the relevant tests and fix failures"
```

Configuration environment variables:

- `MEMAX_CODE_PROVIDER`: default provider, `openai` or `anthropic`.
- `OPENAI_API_KEY`: OpenAI API key.
- `OPENAI_MODEL`: default OpenAI model when `--model` is omitted.
- `ANTHROPIC_API_KEY`: Anthropic API key.
- `ANTHROPIC_MODEL`: default Anthropic model when `--model` is omitted.

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
