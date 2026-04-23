package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const interactiveHistoryMaxEntries = 500

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
	if h.path == "" {
		return nil, nil
	}
	file, err := os.Open(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open prompt history %s: %w", h.path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat prompt history %s: %w", h.path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("prompt history %s is a directory", h.path)
	}

	var entries []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 16*1024), interactiveScannerMaxBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry promptHistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		text := normalizePromptHistoryText(entry.Text)
		if text == "" {
			continue
		}
		if len(entries) > 0 && entries[len(entries)-1] == text {
			continue
		}
		entries = append(entries, text)
		if len(entries) > interactiveHistoryMaxEntries {
			copy(entries, entries[len(entries)-interactiveHistoryMaxEntries:])
			entries = entries[:interactiveHistoryMaxEntries]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read prompt history %s: %w", h.path, err)
	}
	return entries, nil
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
	file, err := os.OpenFile(h.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open prompt history %s: %w", h.path, err)
	}
	defer file.Close()
	entry := promptHistoryEntry{Text: text, Timestamp: time.Now().UTC()}
	if err := json.NewEncoder(file).Encode(entry); err != nil {
		return fmt.Errorf("write prompt history %s: %w", h.path, err)
	}
	return nil
}

func normalizePromptHistoryText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimRight(text, "\n")
}
