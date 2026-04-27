package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runConfigCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printConfigUsage(stdout)
		return nil
	}
	switch args[0] {
	case "init":
		return runConfigInit(args[1:], stdout, stderr)
	case "show":
		return runConfigShow(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printConfigUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown config command %q (want init or show)", args[0])
	}
}

func printConfigUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: memax-code config init [flags]")
	fmt.Fprintln(w, "       memax-code config show [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init    create a JSON config file")
	fmt.Fprintln(w, "  show    print the JSON config file")
}

func runConfigInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("memax-code config init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	force := fs.Bool("force", false, "overwrite an existing config file")
	providerRaw := fs.String("provider", defaultConfigProvider, "model provider: openai or anthropic")
	model := fs.String("model", "", "provider model name")
	profile := fs.String("profile", defaultConfigProfile, "coding model profile: fast, balanced, or deep")
	effort := fs.String("effort", defaultConfigEffort, "reasoning effort: auto, low, medium, high, or xhigh")
	preset := fs.String("preset", defaultConfigPreset, "coding preset: safe_local, ci_repair, or interactive_dev")
	uiRaw := fs.String("ui", defaultConfigUI, "event renderer: auto, app, live, tui, or plain")
	compactionRaw := fs.String("compaction", string(compactionModeAuto), "context compaction mode: auto or off")
	contextWindow := fs.Int("context-window", 0, "approximate model token budget before auto-compaction; 0 infers from provider/model")
	contextSummary := fs.Int("context-summary-tokens", 0, "approximate token budget for compacted context summaries; 0 chooses a default")
	sessionDir := fs.String("session-dir", defaultConfigSessionDir, "directory for JSONL session transcripts")
	historyFile := fs.String("history-file", defaultConfigHistoryFile, "path to interactive prompt history JSONL")
	inheritCommandEnv := fs.Bool("inherit-command-env", true, "let command tools inherit the host process environment")
	noInheritCommandEnv := fs.Bool("no-inherit-command-env", false, "write inherit_command_env=false to disable host environment inheritance")
	webEnabled := fs.Bool("web", true, "write web=true to enable default web tools")
	noWeb := fs.Bool("no-web", false, "write web=false to disable default web tools")
	webFetchMaxBytes := fs.Int("web-fetch-max-bytes", defaultWebFetchMaxBytes, "maximum bytes the default web fetcher reads per URL")
	skillsEnabled := fs.Bool("skills", true, "write skills=true to enable local SKILL.md discovery")
	noSkills := fs.Bool("no-skills", false, "write skills=false to disable local SKILL.md discovery")
	skillDirs := newStringListFlag()
	verifyCommands := newVerifyCommandsFlag()
	fs.Var(skillDirs, "skill-dir", "write a local skills directory containing */SKILL.md; repeat to include multiple dirs")
	fs.Var(verifyCommands, "verify-command", "add a verification command as name=command; repeat for test, lint, typecheck, or default (default wins over test for empty/default requests)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code config init [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("config init does not accept positional arguments")
	}
	configPath, err := resolvePath(*configRaw)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	providerName, err := parseProvider(*providerRaw)
	if err != nil {
		return err
	}
	profileValue, err := parseModelProfile(*profile)
	if err != nil {
		return fmt.Errorf("unknown model profile %q (want one of: %s)", *profile, validModelProfiles())
	}
	effortValue, err := parseModelEffort(*effort)
	if err != nil {
		return fmt.Errorf("unknown model effort %q (want one of: %s)", *effort, validModelEfforts())
	}
	presetValue, err := parsePreset(*preset)
	if err != nil {
		return err
	}
	uiValue, err := parseRenderMode(*uiRaw)
	if err != nil {
		return err
	}
	compactionValue, err := parseCompactionMode(*compactionRaw)
	if err != nil {
		return err
	}
	if *contextWindow < 0 {
		return fmt.Errorf("context-window must be zero or greater")
	}
	if *contextSummary < 0 {
		return fmt.Errorf("context-summary-tokens must be zero or greater")
	}
	if strings.TrimSpace(*sessionDir) == "" {
		return fmt.Errorf("session-dir is required")
	}
	if strings.TrimSpace(*historyFile) == "" {
		return fmt.Errorf("history-file is required")
	}
	cfg := fileConfig{
		Provider:    string(providerName),
		Model:       strings.TrimSpace(*model),
		Profile:     profileValue.String(),
		Effort:      effortValue.String(),
		Preset:      string(presetValue),
		UI:          string(uiValue),
		Compaction:  string(compactionValue),
		SessionDir:  strings.TrimSpace(*sessionDir),
		HistoryFile: strings.TrimSpace(*historyFile),
	}
	if flagWasSet(fs, "context-window") {
		cfg.ContextWindow = intPtr(*contextWindow)
	}
	if flagWasSet(fs, "context-summary-tokens") {
		cfg.ContextSummary = intPtr(*contextSummary)
	}
	noInheritCommandEnvFlagSet := flagWasSet(fs, "no-inherit-command-env")
	if flagWasSet(fs, "inherit-command-env") && noInheritCommandEnvFlagSet {
		return fmt.Errorf("--inherit-command-env cannot be combined with --no-inherit-command-env; choose one")
	}
	if noInheritCommandEnvFlagSet {
		*inheritCommandEnv = !*noInheritCommandEnv
	}
	if flagWasSet(fs, "inherit-command-env") || noInheritCommandEnvFlagSet {
		cfg.InheritCommandEnv = boolPtr(*inheritCommandEnv)
	}
	noWebFlagSet := flagWasSet(fs, "no-web")
	if flagWasSet(fs, "web") && noWebFlagSet {
		return fmt.Errorf("--web cannot be combined with --no-web; choose one")
	}
	if noWebFlagSet {
		*webEnabled = !*noWeb
	}
	if flagWasSet(fs, "web") || noWebFlagSet {
		cfg.Web = boolPtr(*webEnabled)
	}
	if flagWasSet(fs, "web-fetch-max-bytes") {
		if *webFetchMaxBytes <= 0 {
			return fmt.Errorf("web-fetch-max-bytes must be greater than 0")
		}
		cfg.WebFetchMaxBytes = intPtr(normalizedWebFetchMaxBytes(*webFetchMaxBytes))
	}
	noSkillsFlagSet := flagWasSet(fs, "no-skills")
	if flagWasSet(fs, "skills") && noSkillsFlagSet {
		return fmt.Errorf("--skills cannot be combined with --no-skills; choose one")
	}
	if noSkillsFlagSet {
		*skillsEnabled = !*noSkills
	}
	if flagWasSet(fs, "skills") || noSkillsFlagSet {
		cfg.Skills = boolPtr(*skillsEnabled)
	}
	if skillDirs.set {
		cfg.SkillDirs = append([]string(nil), skillDirs.values...)
	}
	if verifyCommands.set {
		cfg.VerifyCommands = cloneStringMap(verifyCommands.values)
	}
	if err := writeConfigFile(configPath, cfg, *force); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created config: %s\n", configPath)
	return nil
}

func runConfigShow(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("memax-code config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: memax-code config show [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("config show does not accept positional arguments")
	}
	configPath, err := resolvePath(*configRaw)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	configExplicit := flagWasSet(fs, "config") || strings.TrimSpace(os.Getenv("MEMAX_CODE_CONFIG")) != ""
	cfg, loaded, err := loadConfig(configPath, configExplicit)
	if err != nil {
		if !configExplicit {
			return fmt.Errorf("%w (fix or remove the default config file, or pass --config with a valid config path)", err)
		}
		return err
	}
	fmt.Fprintf(stdout, "config: %s\n", configPath)
	fmt.Fprintf(stdout, "config_loaded: %t\n", loaded)
	if !loaded {
		return nil
	}
	return writeConfigJSON(stdout, cfg)
}

func writeConfigFile(path string, cfg fileConfig, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("config %s is not a regular file", path)
		}
		if !force {
			return fmt.Errorf("config %s already exists; pass --force to overwrite", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat config %s: %w", path, err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if os.IsExist(err) && !force {
			return fmt.Errorf("config %s already exists; pass --force to overwrite", path)
		}
		return fmt.Errorf("create config %s: %w", path, err)
	}
	defer file.Close()
	if err := writeConfigJSON(file, cfg); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func writeConfigJSON(w io.Writer, cfg fileConfig) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(cfg)
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

const (
	defaultConfigProvider    = "openai"
	defaultConfigProfile     = "balanced"
	defaultConfigEffort      = "auto"
	defaultConfigPreset      = "interactive_dev"
	defaultConfigUI          = "auto"
	defaultConfigSessionDir  = "~/.memax-code/sessions"
	defaultConfigHistoryFile = "~/.memax-code/history.jsonl"
)
