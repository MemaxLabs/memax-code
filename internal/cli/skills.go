package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MemaxLabs/memax-go-agent-sdk/skill"
)

func defaultSkillDirs(cwd string) []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs, filepath.Join(home, ".memax-code", "skills"))
	}
	for _, rel := range []string{
		filepath.Join(".memax-code", "skills"),
		filepath.Join(".agents", "skills"),
		filepath.Join(".claude", "skills"),
		filepath.Join(".codex", "skills"),
	} {
		dirs = append(dirs, filepath.Join(cwd, rel))
	}
	return dedupeStrings(dirs)
}

func skillDirsSetting(flags *stringListFlag, flagSet bool, envKey string, configValue []string, cwd string) ([]string, error) {
	var raw []string
	switch {
	case flagSet && flags != nil:
		raw = flags.values
	case strings.TrimSpace(os.Getenv(envKey)) != "":
		raw = splitPathList(os.Getenv(envKey))
	case len(configValue) > 0:
		raw = configValue
	default:
		raw = defaultSkillDirs(cwd)
	}
	return resolveSkillDirs(raw, cwd)
}

func splitPathList(raw string) []string {
	parts := filepath.SplitList(raw)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func resolveSkillDirs(raw []string, cwd string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, dir := range raw {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		resolved, err := resolveSkillDir(dir, cwd)
		if err != nil {
			return nil, fmt.Errorf("resolve skill dir %q: %w", dir, err)
		}
		out = append(out, resolved)
	}
	return dedupeStrings(out), nil
}

func resolveSkillDir(dir string, cwd string) (string, error) {
	if dir == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return filepath.Abs(dir)
}

func cliSkillSource(dirs []string) skill.Source {
	return skill.SourceFunc(func(ctx context.Context) ([]skill.Skill, error) {
		return loadCLISkills(ctx, dirs)
	})
}

func shouldConfigureSkillRuntime(ctx context.Context, opts options) (bool, error) {
	if !opts.SkillsEnabled {
		return false, nil
	}
	if opts.SkillDirsConfigured {
		return true, nil
	}
	items, err := loadCLISkills(ctx, opts.SkillDirs)
	if err != nil {
		// Keep the skill tools available so /skills or search_skills can surface
		// the concrete loader error instead of silently hiding a broken skill dir.
		return true, nil
	}
	return len(items) > 0, nil
}

func loadCLISkills(ctx context.Context, dirs []string) ([]skill.Skill, error) {
	var out []skill.Skill
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := skill.LoadDir(ctx, dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load skills from %s: %w", dir, err)
		}
		out = append(out, items...)
	}
	return dedupeSkillsLastWins(out), nil
}

func dedupeSkillsLastWins(in []skill.Skill) []skill.Skill {
	indices := map[string]int{}
	out := make([]skill.Skill, 0, len(in))
	for _, item := range in {
		if strings.TrimSpace(item.Name) == "" {
			out = append(out, item)
			continue
		}
		if index, ok := indices[item.Name]; ok {
			out[index] = item
			continue
		}
		indices[item.Name] = len(out)
		out = append(out, item)
	}
	return out
}

func countCLISkills(ctx context.Context, dirs []string) (int, error) {
	items, err := loadCLISkills(ctx, dirs)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func printInteractiveSkills(ctx context.Context, w io.Writer, opts options) {
	if !opts.SkillsEnabled {
		fmt.Fprintln(w, "skills: disabled")
		return
	}
	for _, dir := range opts.SkillDirs {
		fmt.Fprintf(w, "  dir: %s\n", dir)
	}
	items, err := loadCLISkills(ctx, opts.SkillDirs)
	if err != nil {
		fmt.Fprintf(w, "skills: error: %v\n", err)
		return
	}
	fmt.Fprintf(w, "skills: %d loaded\n", len(items))
	if len(items) == 0 {
		return
	}
	for _, item := range items {
		fmt.Fprintf(w, "  - %s", item.Name)
		if item.Description != "" {
			fmt.Fprintf(w, ": %s", item.Description)
		}
		if item.WhenToUse != "" {
			fmt.Fprintf(w, " (use when: %s)", item.WhenToUse)
		}
		if item.Path != "" {
			fmt.Fprintf(w, " [%s]", item.Path)
		}
		fmt.Fprintln(w)
	}
}
