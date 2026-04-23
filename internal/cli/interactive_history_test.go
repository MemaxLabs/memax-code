package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPersistentPromptHistorySkipsBadAndOversizedLines(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	oversized := strings.Repeat("x", interactiveHistoryMaxLineBytes+1)
	body := strings.Join([]string{
		`{"text":"first prompt"}`,
		`not json`,
		oversized,
		`{"text":"second prompt"}`,
	}, "\n")
	if err := os.WriteFile(historyFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	entries, err := newPersistentPromptHistory(historyFile).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := strings.Join(entries, "|"), "first prompt|second prompt"; got != want {
		t.Fatalf("Load() entries = %q, want %q", got, want)
	}
}

func TestPersistentPromptHistorySkipsTrailingOversizedLine(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	oversized := strings.Repeat("x", interactiveHistoryMaxLineBytes+1)
	body := `{"text":"first prompt"}` + "\n" + oversized
	if err := os.WriteFile(historyFile, []byte(body), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	entries, err := newPersistentPromptHistory(historyFile).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := strings.Join(entries, "|"), "first prompt"; got != want {
		t.Fatalf("Load() entries = %q, want %q", got, want)
	}
}

func TestPersistentPromptHistoryEmptyFile(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(historyFile, nil, 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	entries, err := newPersistentPromptHistory(historyFile).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Load() entries = %#v, want empty", entries)
	}
}

func TestPersistentPromptHistoryAppendCompactsOnDisk(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	store := newPersistentPromptHistory(historyFile)
	for i := 0; i < interactiveHistoryCompactThreshold+1; i++ {
		if err := store.Append(fmt.Sprintf("prompt %03d", i)); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}

	entries, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != interactiveHistoryMaxEntries {
		t.Fatalf("Load() entry count = %d, want %d", len(entries), interactiveHistoryMaxEntries)
	}
	if entries[0] != "prompt 126" || entries[len(entries)-1] != "prompt 625" {
		t.Fatalf("Load() kept range %q..%q, want prompt 126..prompt 625", entries[0], entries[len(entries)-1])
	}
	body, err := os.ReadFile(historyFile)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if lines := strings.Count(string(body), "\n"); lines != interactiveHistoryMaxEntries {
		t.Fatalf("history file lines = %d, want %d", lines, interactiveHistoryMaxEntries)
	}
	if strings.Contains(string(body), "prompt 125") {
		t.Fatalf("history file retained compacted prompt:\n%s", body)
	}
}

func TestPersistentPromptHistoryDefersCompactionUntilThreshold(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	store := newPersistentPromptHistory(historyFile)
	for i := 0; i < interactiveHistoryMaxEntries+1; i++ {
		if err := store.Append(fmt.Sprintf("prompt %03d", i)); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}
	assertPromptHistoryLineCount(t, historyFile, interactiveHistoryMaxEntries+1)

	for i := interactiveHistoryMaxEntries + 1; i < interactiveHistoryCompactThreshold+1; i++ {
		if err := store.Append(fmt.Sprintf("prompt %03d", i)); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}
	assertPromptHistoryLineCount(t, historyFile, interactiveHistoryMaxEntries)
}

func TestPersistentPromptHistoryCompactionPreservesTimestamp(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	store := newPersistentPromptHistory(historyFile)
	if err := store.Append("first prompt"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	body, err := os.ReadFile(historyFile)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var before promptHistoryEntry
	if err := json.Unmarshal(bytesFirstLine(body), &before); err != nil {
		t.Fatalf("unmarshal first history entry: %v", err)
	}
	if before.Timestamp.IsZero() {
		t.Fatal("timestamp is zero before compaction")
	}

	if err := os.WriteFile(historyFile, append(body, []byte("not json\n")...), 0o600); err != nil {
		t.Fatalf("write corrupt history: %v", err)
	}
	if err := store.Append("second prompt"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	body, err = os.ReadFile(historyFile)
	if err != nil {
		t.Fatalf("read compacted history: %v", err)
	}
	var after promptHistoryEntry
	if err := json.Unmarshal(bytesFirstLine(body), &after); err != nil {
		t.Fatalf("unmarshal compacted first history entry: %v", err)
	}
	if !after.Timestamp.Equal(before.Timestamp) {
		t.Fatalf("timestamp after compaction = %s, want %s", after.Timestamp, before.Timestamp)
	}
}

func TestPersistentPromptHistoryConcurrentAppends(t *testing.T) {
	historyFile := filepath.Join(t.TempDir(), "history.jsonl")
	store := newPersistentPromptHistory(historyFile)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				if err := store.Append(fmt.Sprintf("worker %d prompt %d", worker, i)); err != nil {
					errs <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	entries, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 20 {
		t.Fatalf("Load() entry count = %d, want 20: %#v", len(entries), entries)
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		seen[entry] = true
	}
	for worker := 0; worker < 4; worker++ {
		for i := 0; i < 5; i++ {
			want := fmt.Sprintf("worker %d prompt %d", worker, i)
			if !seen[want] {
				t.Fatalf("missing prompt %q from entries %#v", want, entries)
			}
		}
	}
}

func bytesFirstLine(body []byte) []byte {
	if index := bytes.IndexByte(body, '\n'); index >= 0 {
		return body[:index]
	}
	return body
}

func assertPromptHistoryLineCount(t *testing.T, historyFile string, want int) {
	t.Helper()
	body, err := os.ReadFile(historyFile)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if got := bytes.Count(body, []byte("\n")); got != want {
		t.Fatalf("history file lines = %d, want %d", got, want)
	}
}
