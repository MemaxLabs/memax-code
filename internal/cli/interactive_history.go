package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	interactiveHistoryMaxEntries         = 500
	interactiveHistoryCompactEntryTarget = interactiveHistoryMaxEntries
	interactiveHistoryCompactThreshold   = interactiveHistoryMaxEntries + interactiveHistoryMaxEntries/4
	interactiveHistoryMaxLineBytes       = 256 * 1024
)

type persistentPromptHistory struct {
	path string
}

type promptHistoryEntry struct {
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

func newPersistentPromptHistory(path string) persistentPromptHistory {
	return persistentPromptHistory{path: strings.TrimSpace(path)}
}

func (h persistentPromptHistory) Load() ([]string, error) {
	result, err := h.loadEntries(interactiveHistoryMaxEntries)
	if err != nil {
		return nil, err
	}
	texts := make([]string, 0, len(result.entries))
	for _, entry := range result.entries {
		texts = append(texts, entry.Text)
	}
	return texts, nil
}

func (h persistentPromptHistory) Append(text string) error {
	if h.path == "" {
		return nil
	}
	text = normalizePromptHistoryText(text)
	if text == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return fmt.Errorf("create prompt history dir: %w", err)
	}
	unlock, err := lockPromptHistory(h.path)
	if err != nil {
		return err
	}
	defer unlock()

	entry := promptHistoryEntry{Text: text, Timestamp: time.Now().UTC()}
	line, err := marshalPromptHistoryEntry(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(h.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open prompt history %s: %w", h.path, err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write prompt history %s: %w", h.path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close prompt history %s: %w", h.path, err)
	}
	if err := h.compactIfNeededLocked(); err != nil {
		return err
	}
	return nil
}

type promptHistoryLoadResult struct {
	entries   []promptHistoryEntry
	skipped   bool
	truncated bool
}

func (h persistentPromptHistory) loadEntries(limit int) (promptHistoryLoadResult, error) {
	if h.path == "" {
		return promptHistoryLoadResult{}, nil
	}
	unlock, err := lockPromptHistoryIfPresent(h.path)
	if err != nil {
		return promptHistoryLoadResult{}, err
	}
	defer unlock()
	return h.loadEntriesLocked(limit)
}

func (h persistentPromptHistory) loadEntriesLocked(limit int) (promptHistoryLoadResult, error) {
	file, err := os.Open(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return promptHistoryLoadResult{}, nil
		}
		return promptHistoryLoadResult{}, fmt.Errorf("open prompt history %s: %w", h.path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return promptHistoryLoadResult{}, fmt.Errorf("stat prompt history %s: %w", h.path, err)
	}
	if info.IsDir() {
		return promptHistoryLoadResult{}, fmt.Errorf("prompt history %s is a directory", h.path)
	}

	var result promptHistoryLoadResult
	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		line, ok, skippedLine, err := readPromptHistoryLine(reader)
		if err != nil {
			return promptHistoryLoadResult{}, fmt.Errorf("read prompt history %s: %w", h.path, err)
		}
		if !ok {
			break
		}
		if skippedLine {
			result.skipped = true
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry promptHistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			result.skipped = true
			continue
		}
		text := normalizePromptHistoryText(entry.Text)
		if text == "" {
			continue
		}
		if len(result.entries) > 0 && result.entries[len(result.entries)-1].Text == text {
			result.skipped = true
			continue
		}
		entry.Text = text
		result.entries = append(result.entries, entry)
		if limit > 0 && len(result.entries) > limit {
			copy(result.entries, result.entries[len(result.entries)-limit:])
			result.entries = result.entries[:limit]
			result.truncated = true
		}
	}
	return result, nil
}

func (h persistentPromptHistory) compactIfNeededLocked() error {
	result, err := h.loadEntriesLocked(interactiveHistoryCompactThreshold)
	if err != nil {
		return err
	}
	if !result.skipped && !result.truncated {
		return nil
	}
	entries := result.entries
	if len(entries) > interactiveHistoryCompactEntryTarget {
		entries = entries[len(entries)-interactiveHistoryCompactEntryTarget:]
	}
	temp, err := os.CreateTemp(filepath.Dir(h.path), ".history-*.jsonl")
	if err != nil {
		return fmt.Errorf("create prompt history temp file: %w", err)
	}
	tempName := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempName)
		}
	}()
	for _, entry := range entries {
		line, err := marshalPromptHistoryEntry(entry)
		if err != nil {
			_ = temp.Close()
			return err
		}
		if _, err := temp.Write(append(line, '\n')); err != nil {
			_ = temp.Close()
			return fmt.Errorf("write prompt history temp file: %w", err)
		}
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close prompt history temp file: %w", err)
	}
	if err := os.Rename(tempName, h.path); err != nil {
		return fmt.Errorf("replace prompt history %s: %w", h.path, err)
	}
	cleanup = false
	return nil
}

func marshalPromptHistoryEntry(entry promptHistoryEntry) ([]byte, error) {
	line, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("encode prompt history entry: %w", err)
	}
	if len(line) > interactiveHistoryMaxLineBytes {
		return nil, fmt.Errorf("prompt history entry exceeds %d bytes", interactiveHistoryMaxLineBytes)
	}
	return line, nil
}

func readPromptHistoryLine(reader *bufio.Reader) (string, bool, bool, error) {
	var line []byte
	skipping := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 && !skipping {
			if len(line)+len(fragment) > interactiveHistoryMaxLineBytes {
				skipping = true
				line = line[:0]
			} else {
				line = append(line, fragment...)
			}
		}
		switch {
		case err == nil:
			if skipping {
				return "", true, true, nil
			}
			return string(line), true, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if skipping {
				return "", true, true, nil
			}
			if len(line) == 0 {
				return "", false, false, nil
			}
			return string(line), true, false, nil
		default:
			return "", false, false, err
		}
	}
}

func normalizePromptHistoryText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimRight(text, "\n")
}
