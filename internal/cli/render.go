package cli

import (
	"fmt"
	"io"
	"strings"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

func renderEvents(w io.Writer, events <-chan memaxagent.Event) error {
	var firstErr error
	state := renderState{}
	for event := range events {
		if err := state.render(w, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type renderState struct {
	assistantLineOpen bool
}

func (s *renderState) render(w io.Writer, event memaxagent.Event) error {
	if event.Kind != memaxagent.EventAssistant && event.Kind != memaxagent.EventToolUseDelta {
		s.closeAssistantLine(w)
	}
	err := renderEvent(w, event)
	if event.Kind == memaxagent.EventAssistant && event.Message != nil {
		text := event.Message.PlainText()
		if text != "" {
			s.assistantLineOpen = !strings.HasSuffix(text, "\n")
		}
	}
	return err
}

func (s *renderState) closeAssistantLine(w io.Writer) {
	if s.assistantLineOpen {
		fmt.Fprintln(w)
		s.assistantLineOpen = false
	}
}

func renderEvent(w io.Writer, event memaxagent.Event) error {
	switch event.Kind {
	case memaxagent.EventSessionStarted:
		fmt.Fprintf(w, "session: %s\n", event.SessionID)
	case memaxagent.EventAssistant:
		if event.Message != nil {
			text := event.Message.PlainText()
			fmt.Fprintf(w, "%s", text)
		}
	case memaxagent.EventToolUseStart:
		if event.ToolUse != nil {
			fmt.Fprintf(w, "\ntool_start: %s\n", event.ToolUse.Name)
		}
	case memaxagent.EventToolUseDelta:
		// Deltas arrive token-by-token and are useful for protocol observers,
		// not for the default terminal transcript. The finalized tool-use event
		// prints the actionable call.
	case memaxagent.EventToolUse:
		if event.ToolUse != nil {
			fmt.Fprintf(w, "\ntool: %s\n", event.ToolUse.Name)
		}
	case memaxagent.EventToolResult:
		if event.ToolResult != nil {
			content := strings.TrimSpace(event.ToolResult.Content)
			label := "tool_result"
			if event.ToolResult.IsError {
				label = "tool_error"
				if content == "" {
					content = "<empty tool error>"
				}
			}
			if content != "" {
				fmt.Fprintf(w, "%s: %s\n", label, content)
			}
		}
	case memaxagent.EventWorkspaceCheckpoint:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_checkpoint: %s\n", event.Workspace.CheckpointID)
		}
	case memaxagent.EventWorkspacePatch:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_patch: paths=%s changes=%d\n", strings.Join(event.Workspace.Paths, ","), event.Workspace.Changes)
		}
	case memaxagent.EventWorkspaceDiff:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_diff: paths=%s changes=%d\n", strings.Join(event.Workspace.Paths, ","), event.Workspace.Changes)
		}
	case memaxagent.EventWorkspaceRestore:
		if event.Workspace != nil {
			fmt.Fprintf(w, "workspace_restore: %s\n", event.Workspace.CheckpointID)
		}
	case memaxagent.EventCommandFinished:
		if event.Command != nil {
			command := strings.TrimSpace(event.Command.Command)
			if command == "" {
				command = strings.Join(event.Command.Argv, " ")
			}
			fmt.Fprintf(w, "command: %s exit=%d timeout=%t\n", command, event.Command.ExitCode, event.Command.TimedOut)
		}
	case memaxagent.EventCommandStarted:
		if event.Command != nil {
			fmt.Fprintf(w, "command_started: %s pid=%d\n", event.Command.CommandID, event.Command.PID)
		}
	case memaxagent.EventCommandOutput:
		if event.Command != nil {
			fmt.Fprintf(w, "command_output: %s chunks=%d next_seq=%d\n", event.Command.CommandID, event.Command.OutputChunks, event.Command.NextSeq)
		}
	case memaxagent.EventCommandInput:
		if event.Command != nil {
			fmt.Fprintf(w, "command_input: %s bytes=%d\n", event.Command.CommandID, event.Command.InputBytes)
		}
	case memaxagent.EventCommandResized:
		if event.Command != nil {
			fmt.Fprintf(w, "command_resized: %s cols=%d rows=%d\n", event.Command.CommandID, event.Command.Cols, event.Command.Rows)
		}
	case memaxagent.EventCommandStopped:
		if event.Command != nil {
			fmt.Fprintf(w, "command_stopped: %s status=%s\n", event.Command.CommandID, event.Command.Status)
		}
	case memaxagent.EventVerification:
		if event.Verification != nil {
			fmt.Fprintf(w, "verification: %s passed=%t\n", event.Verification.Name, event.Verification.Passed)
		}
	case memaxagent.EventUsage:
		if event.Usage != nil {
			fmt.Fprintf(w, "usage: input=%d output=%d total=%d\n", event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.TotalTokens)
		}
	case memaxagent.EventResult:
		if strings.TrimSpace(event.Result) != "" {
			fmt.Fprintf(w, "\nresult: %s\n", event.Result)
		}
	case memaxagent.EventError:
		if event.Err == nil {
			return fmt.Errorf("agent emitted error event")
		}
		return event.Err
	}
	return nil
}
