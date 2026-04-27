package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type doctorLevel string

const (
	doctorOK   doctorLevel = "ok"
	doctorWarn doctorLevel = "warn"
	doctorFail doctorLevel = "fail"
)

type doctorCheck struct {
	Level   doctorLevel
	Name    string
	Message string
}

func runDoctorCommand(args []string, stdout, stderr io.Writer) error {
	if doctorHelpRequested(args) {
		printDoctorUsage(stdout)
		return nil
	}
	if unsupported := unsupportedDoctorFlag(args); unsupported != "" {
		return printDoctorFailure(stdout, "arguments", fmt.Sprintf("doctor does not accept %s", unsupported))
	}
	opts, err := parseArgs(args, io.Discard)
	if err != nil {
		return printDoctorFailure(stdout, "arguments", err.Error())
	}
	if strings.TrimSpace(opts.Prompt) != "" {
		return printDoctorFailure(stdout, "arguments", "doctor does not accept a prompt")
	}
	checks := doctorChecks(opts)
	fmt.Fprintln(stdout, "Memax Code doctor")
	failures, warnings := 0, 0
	for _, check := range checks {
		switch check.Level {
		case doctorFail:
			failures++
		case doctorWarn:
			warnings++
		}
		fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Level, check.Name, check.Message)
	}
	fmt.Fprintf(stdout, "summary: %d fail, %d warn\n", failures, warnings)
	if failures == 0 && warnings == 0 {
		fmt.Fprintln(stdout, "all checks passed")
	}
	if failures > 0 {
		return fmt.Errorf("doctor found %d failure(s)", failures)
	}
	return nil
}

func doctorHelpRequested(args []string) bool {
	skipValue := false
	for i, arg := range args {
		if skipValue {
			skipValue = false
			continue
		}
		if i == 0 && arg == "help" {
			return true
		}
		name, _, hasInlineValue := strings.Cut(arg, "=")
		if name == "-h" || name == "-help" || name == "--help" {
			return true
		}
		if !hasInlineValue && doctorFlagTakesValue(name) {
			skipValue = true
		}
	}
	return false
}

func unsupportedDoctorFlag(args []string) string {
	skipValue := false
	for _, arg := range args {
		if skipValue {
			skipValue = false
			continue
		}
		name, _, ok := strings.Cut(arg, "=")
		if !ok {
			name = arg
		}
		switch name {
		case "--dry-run", "--inspect-tools", "--list-sessions", "--resume", "--show-session":
			return name
		}
		if !ok && doctorFlagTakesValue(name) {
			skipValue = true
		}
	}
	return ""
}

func doctorFlagTakesValue(name string) bool {
	switch name {
	case "--config", "--cwd", "--provider", "--model", "--profile", "--effort", "--preset", "--ui", "--session-dir", "--history-file", "--verify-command", "-C", "-cd", "--C", "--cd":
		return true
	default:
		return false
	}
}

func printDoctorFailure(stdout io.Writer, name, message string) error {
	fmt.Fprintln(stdout, "Memax Code doctor")
	fmt.Fprintf(stdout, "[%s] %s: %s\n", doctorFail, name, message)
	fmt.Fprintln(stdout, "summary: 1 fail, 0 warn")
	return fmt.Errorf("doctor found 1 failure")
}

func printDoctorUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: memax-code doctor [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Runs local setup diagnostics without calling a model.")
	fmt.Fprintln(w, "Common flags: --config, --cwd, --provider, --model, --profile, --effort, --preset, --ui, --session-dir, --history-file, --verify-command")
}

func doctorChecks(opts options) []doctorCheck {
	mode := verificationMode(opts.CWD, opts.VerifyCommands)
	checks := []doctorCheck{
		configDoctorCheck(opts),
		{Level: doctorOK, Name: "cwd", Message: opts.CWD},
		{Level: doctorOK, Name: "provider", Message: string(opts.Provider)},
		modelDoctorCheck(opts),
		apiKeyDoctorCheck(opts),
		sessionDirDoctorCheck(opts.SessionDir),
		historyFileDoctorCheck(opts.HistoryFile),
		skillsDoctorCheck(opts),
		verificationDoctorCheck(mode, opts.VerifyCommands),
		commandDoctorCheck("git", false),
		commandDoctorCheck("go", mode == "go"),
	}
	return checks
}

func skillsDoctorCheck(opts options) doctorCheck {
	if !opts.SkillsEnabled {
		if opts.SkillDirsConfigured {
			return doctorCheck{Level: doctorWarn, Name: "skills", Message: "disabled; configured skill directories ignored"}
		}
		return doctorCheck{Level: doctorOK, Name: "skills", Message: "disabled"}
	}
	count, err := countCLISkills(context.Background(), opts.SkillDirs)
	if err != nil {
		return doctorCheck{Level: doctorFail, Name: "skills", Message: err.Error()}
	}
	if count == 0 {
		return doctorCheck{Level: doctorOK, Name: "skills", Message: "enabled; no local skills found"}
	}
	return doctorCheck{Level: doctorOK, Name: "skills", Message: fmt.Sprintf("enabled; %d loaded", count)}
}

func configDoctorCheck(opts options) doctorCheck {
	if opts.ConfigLoaded {
		return doctorCheck{Level: doctorOK, Name: "config", Message: opts.ConfigPath + " (loaded)"}
	}
	return doctorCheck{Level: doctorWarn, Name: "config", Message: opts.ConfigPath + " (not found; defaults/env/flags only)"}
}

func modelDoctorCheck(opts options) doctorCheck {
	if strings.TrimSpace(opts.Model) == "" {
		return doctorCheck{
			Level:   doctorWarn,
			Name:    "model",
			Message: fmt.Sprintf("<unset>; pass --model, set %s, or write model in config", opts.Provider.modelEnv()),
		}
	}
	return doctorCheck{Level: doctorOK, Name: "model", Message: opts.Model}
}

func apiKeyDoctorCheck(opts options) doctorCheck {
	key := opts.Provider.keyEnv()
	if strings.TrimSpace(os.Getenv(key)) == "" {
		return doctorCheck{Level: doctorWarn, Name: "api_key", Message: key + " is not set"}
	}
	return doctorCheck{Level: doctorOK, Name: "api_key", Message: key + " is set"}
}

func sessionDirDoctorCheck(path string) doctorCheck {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return doctorCheck{Level: doctorFail, Name: "session_dir", Message: path + " is not a directory"}
		}
		return doctorCheck{Level: doctorOK, Name: "session_dir", Message: path}
	}
	if !os.IsNotExist(err) {
		return doctorCheck{Level: doctorFail, Name: "session_dir", Message: fmt.Sprintf("stat %s: %v", path, err)}
	}
	parent := filepath.Dir(path)
	if info, err := os.Stat(parent); err == nil && info.IsDir() {
		return doctorCheck{Level: doctorWarn, Name: "session_dir", Message: path + " (missing; will be created on first write)"}
	}
	return doctorCheck{Level: doctorWarn, Name: "session_dir", Message: path + " (missing; parent directory is not present)"}
}

func historyFileDoctorCheck(path string) doctorCheck {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return doctorCheck{Level: doctorFail, Name: "history_file", Message: path + " is a directory"}
		}
		return doctorCheck{Level: doctorOK, Name: "history_file", Message: path}
	}
	if !os.IsNotExist(err) {
		return doctorCheck{Level: doctorFail, Name: "history_file", Message: fmt.Sprintf("stat %s: %v", path, err)}
	}
	parent := filepath.Dir(path)
	if info, err := os.Stat(parent); err == nil && info.IsDir() {
		return doctorCheck{Level: doctorWarn, Name: "history_file", Message: path + " (missing; will be created on first successful interactive prompt)"}
	}
	return doctorCheck{Level: doctorWarn, Name: "history_file", Message: path + " (missing; parent directory is not present)"}
}

func verificationDoctorCheck(mode string, commands map[string]string) doctorCheck {
	switch mode {
	case "custom":
		names := sortedMapKeys(commands)
		if len(names) == 0 {
			return doctorCheck{Level: doctorOK, Name: "verification", Message: "custom commands configured"}
		}
		return doctorCheck{Level: doctorOK, Name: "verification", Message: "custom commands configured: " + strings.Join(names, ", ")}
	case "go":
		return doctorCheck{Level: doctorOK, Name: "verification", Message: "go workspace detected"}
	default:
		return doctorCheck{Level: doctorWarn, Name: "verification", Message: "disabled; no root go.mod detected"}
	}
}

func commandDoctorCheck(name string, required bool) doctorCheck {
	path, err := exec.LookPath(name)
	if err != nil {
		if required {
			return doctorCheck{Level: doctorFail, Name: "command." + name, Message: "not found in PATH"}
		}
		return doctorCheck{Level: doctorWarn, Name: "command." + name, Message: "not found in PATH"}
	}
	return doctorCheck{Level: doctorOK, Name: "command." + name, Message: path}
}
