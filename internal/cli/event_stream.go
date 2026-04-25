package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	memaxagent "github.com/MemaxLabs/memax-go-agent-sdk"
)

type eventStreamMode string

const (
	eventStreamModeOff  eventStreamMode = ""
	eventStreamModeJSON eventStreamMode = "json"
)

func parseEventStreamMode(raw string) (eventStreamMode, error) {
	switch mode := eventStreamMode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case eventStreamModeOff:
		return eventStreamModeOff, nil
	case eventStreamModeJSON:
		return eventStreamModeJSON, nil
	default:
		return "", fmt.Errorf("unknown event stream %q (want one of: json)", raw)
	}
}

func renderEventStreamObserved(w io.Writer, events <-chan memaxagent.Event, mode eventStreamMode, observe func(memaxagent.Event)) error {
	switch mode {
	case eventStreamModeOff:
		return fmt.Errorf("event stream mode is required")
	case eventStreamModeJSON:
		enc := json.NewEncoder(w)
		for event := range events {
			if observe != nil {
				observe(event)
			}
			if err := enc.Encode(projectStreamEvent(event)); err != nil {
				return fmt.Errorf("encode event stream: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported event stream mode %q", mode)
	}
}

type streamEvent struct {
	Type            string         `json:"type"`
	Time            string         `json:"time,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	ParentSessionID string         `json:"parent_session_id,omitempty"`
	Turn            int            `json:"turn,omitempty"`
	Text            string         `json:"text,omitempty"`
	Delta           string         `json:"delta,omitempty"`
	Result          string         `json:"result,omitempty"`
	Error           string         `json:"error,omitempty"`
	Message         map[string]any `json:"message,omitempty"`
	ToolUse         map[string]any `json:"tool_use,omitempty"`
	ToolResult      map[string]any `json:"tool_result,omitempty"`
	Usage           map[string]int `json:"usage,omitempty"`
	Context         map[string]int `json:"context,omitempty"`
	Workspace       map[string]any `json:"workspace,omitempty"`
	Verification    map[string]any `json:"verification,omitempty"`
	Approval        map[string]any `json:"approval,omitempty"`
	Tenant          map[string]any `json:"tenant,omitempty"`
	Command         map[string]any `json:"command,omitempty"`
	Run             map[string]any `json:"run,omitempty"`
	Notification    map[string]any `json:"notification,omitempty"`
	Skill           map[string]any `json:"skill,omitempty"`
	Memory          map[string]any `json:"memory,omitempty"`
	Compaction      map[string]any `json:"compaction,omitempty"`
}

func projectStreamEvent(event memaxagent.Event) streamEvent {
	out := streamEvent{
		Type:            string(event.Kind),
		SessionID:       event.SessionID,
		ParentSessionID: event.ParentSessionID,
		Turn:            event.Turn,
	}
	if !event.Time.IsZero() {
		out.Time = event.Time.UTC().Format(time.RFC3339Nano)
	}
	switch event.Kind {
	case memaxagent.EventAssistant:
		if event.Message != nil {
			out.Text = event.Message.PlainText()
			out.Message = map[string]any{
				"role": event.Message.Role,
				"text": event.Message.PlainText(),
			}
		}
	case memaxagent.EventToolUseStart, memaxagent.EventToolUse:
		if event.ToolUse != nil {
			out.ToolUse = map[string]any{
				"id":    event.ToolUse.ID,
				"name":  event.ToolUse.Name,
				"input": json.RawMessage(event.ToolUse.Input),
			}
		}
	case memaxagent.EventToolUseDelta:
		out.Delta = event.ToolUseDelta
	case memaxagent.EventToolResult:
		if event.ToolResult != nil {
			out.ToolResult = map[string]any{
				"tool_use_id": event.ToolResult.ToolUseID,
				"name":        event.ToolResult.Name,
				"content":     event.ToolResult.Content,
				"is_error":    event.ToolResult.IsError,
			}
			if len(event.ToolResult.Metadata) > 0 {
				out.ToolResult["metadata"] = event.ToolResult.Metadata
			}
		}
	case memaxagent.EventUsage:
		if event.Usage != nil {
			out.Usage = map[string]int{
				"input_tokens":  event.Usage.InputTokens,
				"output_tokens": event.Usage.OutputTokens,
				"total_tokens":  event.Usage.TotalTokens,
			}
		}
	case memaxagent.EventContextApplied:
		if event.Context != nil {
			out.Context = map[string]int{
				"original_messages": event.Context.OriginalMessages,
				"sent_messages":     event.Context.SentMessages,
			}
		}
	case memaxagent.EventContextCompacted:
		if event.Compaction != nil {
			out.Compaction = map[string]any{
				"policy":              event.Compaction.Policy,
				"reason":              event.Compaction.Reason,
				"original_messages":   event.Compaction.OriginalMessages,
				"sent_messages":       event.Compaction.SentMessages,
				"summarized_messages": event.Compaction.SummarizedMessages,
				"retained_messages":   event.Compaction.RetainedMessages,
				"replaced_summaries":  event.Compaction.ReplacedSummaries,
				"summary_hash":        event.Compaction.SummaryHash,
				"summary_preview":     event.Compaction.SummaryPreview,
			}
		}
	case memaxagent.EventMemoryCandidates:
		if event.Memory != nil {
			out.Memory = map[string]any{
				"count":      len(event.Memory.Candidates),
				"candidates": event.Memory.Candidates,
			}
		}
	case memaxagent.EventSkillDiscovery, memaxagent.EventSkillSearch, memaxagent.EventSkillLoaded, memaxagent.EventSkillResourceLoaded:
		if event.Skill != nil {
			out.Skill = map[string]any{
				"action":          event.Skill.Action,
				"skill_name":      event.Skill.SkillName,
				"resource_name":   event.Skill.ResourceName,
				"query":           event.Skill.Query,
				"selected_skills": event.Skill.SelectedSkills,
				"selected":        event.Skill.Selected,
				"omitted":         event.Skill.Omitted,
				"matches":         event.Skill.Matches,
				"prompt_bytes":    event.Skill.PromptBytes,
				"metadata_only":   event.Skill.MetadataOnly,
			}
		}
	case memaxagent.EventWorkspacePatch, memaxagent.EventWorkspaceDiff, memaxagent.EventWorkspaceCheckpoint, memaxagent.EventWorkspaceRestore:
		if event.Workspace != nil {
			out.Workspace = map[string]any{
				"operation":     event.Workspace.Operation,
				"paths":         event.Workspace.Paths,
				"changes":       event.Workspace.Changes,
				"added":         event.Workspace.Added,
				"modified":      event.Workspace.Modified,
				"deleted":       event.Workspace.Deleted,
				"byte_delta":    event.Workspace.ByteDelta,
				"checkpoint_id": event.Workspace.CheckpointID,
				"base_id":       event.Workspace.BaseID,
			}
		}
	case memaxagent.EventVerification:
		if event.Verification != nil {
			out.Verification = map[string]any{
				"operation":   event.Verification.Operation,
				"name":        event.Verification.Name,
				"passed":      event.Verification.Passed,
				"diagnostics": event.Verification.Diagnostics,
				"paths":       event.Verification.Paths,
			}
		}
	case memaxagent.EventApprovalRequested, memaxagent.EventApprovalGranted, memaxagent.EventApprovalDenied, memaxagent.EventApprovalConsumed:
		if event.Approval != nil {
			out.Approval = map[string]any{
				"action":      event.Approval.Action,
				"reason":      event.Approval.Reason,
				"input_hash":  event.Approval.InputHash,
				"requested":   event.Approval.Requested,
				"approved":    event.Approval.Approved,
				"consumed":    event.Approval.Consumed,
				"single_use":  event.Approval.SingleUse,
				"input_bound": event.Approval.InputBound,
				"summary": map[string]any{
					"title":       event.Approval.Summary.Title,
					"description": event.Approval.Summary.Description,
					"risk":        event.Approval.Summary.Risk,
					"paths":       event.Approval.Summary.Paths,
					"changes":     event.Approval.Summary.Changes,
					"added":       event.Approval.Summary.Added,
					"modified":    event.Approval.Summary.Modified,
					"deleted":     event.Approval.Summary.Deleted,
					"byte_delta":  event.Approval.Summary.ByteDelta,
				},
			}
		}
	case memaxagent.EventTenantDenied:
		if event.Tenant != nil {
			out.Tenant = map[string]any{
				"boundary":   event.Tenant.Boundary,
				"tenant_id":  event.Tenant.TenantID,
				"subject_id": event.Tenant.SubjectID,
				"attributes": event.Tenant.Attributes,
				"reason":     event.Tenant.Reason,
			}
		}
	case memaxagent.EventCommandStarted, memaxagent.EventCommandFinished, memaxagent.EventCommandOutput, memaxagent.EventCommandInput, memaxagent.EventCommandStopped, memaxagent.EventCommandResized:
		if event.Command != nil {
			out.Command = map[string]any{
				"operation":            event.Command.Operation,
				"command_id":           event.Command.CommandID,
				"command":              event.Command.Command,
				"argv":                 event.Command.Argv,
				"cwd":                  event.Command.CWD,
				"status":               event.Command.Status,
				"pid":                  event.Command.PID,
				"tty":                  event.Command.TTY,
				"signals_process_tree": event.Command.SignalsProcessTree,
				"cols":                 event.Command.Cols,
				"rows":                 event.Command.Rows,
				"input_bytes":          event.Command.InputBytes,
				"exit_code":            event.Command.ExitCode,
				"timed_out":            event.Command.TimedOut,
				"duration_ms":          event.Command.DurationMS,
				"stdout_bytes":         event.Command.StdoutBytes,
				"stderr_bytes":         event.Command.StderrBytes,
				"output_truncated":     event.Command.OutputTruncated,
				"next_seq":             event.Command.NextSeq,
				"resume_after_seq":     event.Command.ResumeAfterSeq,
				"output_chunks":        event.Command.OutputChunks,
				"dropped_chunks":       event.Command.DroppedChunks,
				"dropped_bytes":        event.Command.DroppedBytes,
			}
		}
	case memaxagent.EventRunStateChanged:
		if event.Run != nil {
			out.Run = map[string]any{
				"run_id":        event.Run.RunID,
				"status":        event.Run.Status,
				"prompt":        event.Run.Prompt,
				"worker_id":     event.Run.WorkerID,
				"trigger_name":  event.Run.TriggerName,
				"occurrence_at": formatOptionalTime(event.Run.OccurrenceAt),
				"result":        event.Run.Result,
				"error":         event.Run.Error,
			}
		}
	case memaxagent.EventScheduledRunNotificationClaimed, memaxagent.EventScheduledRunNotificationDelivered, memaxagent.EventScheduledRunNotificationFailed, memaxagent.EventScheduledRunNotificationDeadLettered, memaxagent.EventScheduledRunNotificationRequeued:
		if event.Notification != nil {
			out.Notification = map[string]any{
				"notification_id": event.Notification.NotificationID,
				"run_id":          event.Notification.RunID,
				"status":          event.Notification.Status,
				"trigger_name":    event.Notification.TriggerName,
				"occurrence_at":   formatOptionalTime(event.Notification.OccurrenceAt),
				"delivery_status": event.Notification.DeliveryStatus,
				"worker_id":       event.Notification.WorkerID,
				"attempts":        event.Notification.Attempts,
				"delivery_error":  event.Notification.DeliveryError,
				"deliver_after":   formatOptionalTime(event.Notification.DeliverAfter),
				"delivered_at":    formatOptionalTime(event.Notification.DeliveredAt),
				"updated_at":      formatOptionalTime(event.Notification.DeliveryUpdatedAt),
			}
		}
	case memaxagent.EventResult:
		out.Result = event.Result
	case memaxagent.EventError:
		if event.Err != nil {
			out.Error = event.Err.Error()
		}
	}
	return out
}

func formatOptionalTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339Nano)
}
