package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Run parses CLI arguments and executes the requested command using empty stdin.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return RunWithIO(ctx, args, strings.NewReader(""), stdout, stderr)
}

// RunWithIO parses CLI arguments and executes the requested command using the
// supplied standard streams.
func RunWithIO(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "config" {
		return runConfigCommand(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "doctor" {
		return runDoctorCommand(args[1:], stdout, stderr)
	}
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return err
	}
	if opts.ListSessions {
		return listSessions(ctx, stdout, opts)
	}
	if opts.ShowSessionID != "" {
		return showSession(ctx, stdout, opts)
	}
	if opts.InspectTools {
		return inspectTools(ctx, stdout, opts)
	}
	if opts.ResumeSessionID != "" {
		if err := resolveResumeSession(ctx, &opts); err != nil {
			return err
		}
	}
	if opts.Interactive {
		return runInteractive(ctx, stdin, stdout, stderr, opts)
	}
	if opts.Prompt == "" && !opts.DryRun {
		return fmt.Errorf("prompt is required unless --dry-run, --interactive, or --list-sessions is set")
	}
	if opts.DryRun {
		return renderDryRun(stdout, opts)
	}
	return runPrompt(ctx, stdout, opts)
}

type options struct {
	Prompt            string
	CWD               string
	Provider          provider
	Model             string
	Profile           string
	Effort            string
	Preset            string
	UI                renderMode
	ConfigPath        string
	ConfigLoaded      bool
	SessionDir        string
	ResumeSessionID   string
	ListSessions      bool
	ShowSessionID     string
	InspectTools      bool
	DryRun            bool
	Interactive       bool
	InheritCommandEnv bool
}

func parseArgs(args []string, output io.Writer) (options, error) {
	fs := flag.NewFlagSet("memax-code", flag.ContinueOnError)
	fs.SetOutput(output)

	var opts options
	defaultCWD, err := os.Getwd()
	if err != nil {
		return options{}, fmt.Errorf("get cwd: %w", err)
	}
	cwd := &stringFlag{value: defaultCWD}
	configRaw := fs.String("config", envDefault("MEMAX_CODE_CONFIG", defaultConfigPath()), "path to JSON config file")
	providerRaw := fs.String("provider", string(providerOpenAI), "model provider: openai or anthropic")
	model := fs.String("model", "", "provider model name; defaults to OPENAI_MODEL or ANTHROPIC_MODEL")
	profile := fs.String("profile", "", "coding model profile: fast, balanced, or deep")
	effort := fs.String("effort", "", "override reasoning effort: auto, low, medium, high, or xhigh")
	preset := fs.String("preset", "interactive_dev", "coding preset: safe_local, ci_repair, or interactive_dev")
	uiRaw := fs.String("ui", string(renderModeAuto), "event renderer: auto, live, tui, or plain")
	sessionDir := fs.String("session-dir", defaultSessionDir(), "directory for JSONL session transcripts")
	resumeSessionID := fs.String("resume", "", "resume an existing session id, or latest")
	listSessionsFlag := fs.Bool("list-sessions", false, "list saved sessions and exit")
	showSessionID := fs.String("show-session", "", "print a saved session transcript and exit")
	inspectTools := fs.Bool("inspect-tools", false, "print the model-facing tool contract and exit")
	interactive := false
	fs.BoolVar(&interactive, "interactive", false, "start a line-oriented interactive shell")
	fs.BoolVar(&interactive, "i", false, "alias for --interactive")
	fs.Var(cwd, "C", "alias for --cwd")
	fs.Var(cwd, "cd", "alias for --cwd")
	fs.Var(cwd, "cwd", "workspace root")
	dryRun := fs.Bool("dry-run", false, "print resolved configuration without calling a provider")
	inheritCommandEnv := fs.Bool("inherit-command-env", false, "let command tools inherit the host process environment")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: memax-code [flags] PROMPT\n")
		fmt.Fprintf(fs.Output(), "       memax-code --interactive [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --resume SESSION_ID|latest [flags] PROMPT\n")
		fmt.Fprintf(fs.Output(), "       memax-code --list-sessions [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --show-session SESSION_ID|latest [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --inspect-tools [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --dry-run [flags] [PROMPT]\n\n")
		fmt.Fprintf(fs.Output(), "       memax-code config init|show [flags]\n\n")
		fmt.Fprintf(fs.Output(), "       memax-code doctor [flags]\n\n")
		fmt.Fprintf(fs.Output(), "Flags must precede PROMPT because Go flag parsing stops at the first positional argument.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	configPath, err := resolvePath(*configRaw)
	if err != nil {
		return options{}, fmt.Errorf("resolve config path: %w", err)
	}
	configExplicit := flagWasSet(fs, "config") || strings.TrimSpace(os.Getenv("MEMAX_CODE_CONFIG")) != ""
	cfg, configLoaded, err := loadConfig(configPath, configExplicit)
	if err != nil {
		if !configExplicit {
			return options{}, fmt.Errorf("%w (fix or remove the default config file, or pass --config with a valid config path)", err)
		}
		return options{}, err
	}
	sessionDirSetting := stringSetting(*sessionDir, flagWasSet(fs, "session-dir"), "MEMAX_CODE_SESSION_DIR", cfg.SessionDir, defaultSessionDir())
	sessionDirRaw := sessionDirSetting.Value
	resolvedSessionDir, err := resolvePath(sessionDirRaw)
	if err != nil {
		return options{}, fmt.Errorf("resolve session dir: %w", err)
	}
	showSession := strings.TrimSpace(*showSessionID)
	if showSession != "" {
		if *listSessionsFlag {
			return options{}, fmt.Errorf("--show-session cannot be combined with --list-sessions")
		}
		if *inspectTools {
			return options{}, fmt.Errorf("--show-session cannot be combined with --inspect-tools")
		}
		if strings.TrimSpace(*resumeSessionID) != "" {
			return options{}, fmt.Errorf("--show-session cannot be combined with --resume")
		}
		if *dryRun {
			return options{}, fmt.Errorf("--show-session cannot be combined with --dry-run")
		}
		if interactive {
			return options{}, fmt.Errorf("--show-session cannot be combined with --interactive")
		}
		if len(fs.Args()) > 0 {
			return options{}, fmt.Errorf("--show-session does not accept a prompt")
		}
		return options{
			SessionDir:    resolvedSessionDir,
			ShowSessionID: showSession,
		}, nil
	}
	if *listSessionsFlag {
		if *inspectTools {
			return options{}, fmt.Errorf("--list-sessions cannot be combined with --inspect-tools")
		}
		if strings.TrimSpace(*resumeSessionID) != "" {
			return options{}, fmt.Errorf("--list-sessions cannot be combined with --resume")
		}
		if *dryRun {
			return options{}, fmt.Errorf("--list-sessions cannot be combined with --dry-run")
		}
		if interactive {
			return options{}, fmt.Errorf("--list-sessions cannot be combined with --interactive")
		}
		return options{
			SessionDir:   resolvedSessionDir,
			ListSessions: true,
		}, nil
	}

	providerSetting := stringSetting(*providerRaw, flagWasSet(fs, "provider"), "MEMAX_CODE_PROVIDER", cfg.Provider, string(providerOpenAI))
	providerName, err := parseProvider(providerSetting.Value)
	if err != nil {
		if providerSetting.Source == settingSourceEnv {
			return options{}, fmt.Errorf("invalid MEMAX_CODE_PROVIDER: %w", err)
		}
		if providerSetting.Source == settingSourceConfig {
			return options{}, fmt.Errorf("invalid config %s provider: %w", configPath, err)
		}
		return options{}, err
	}
	absCWD, err := filepath.Abs(strings.TrimSpace(cwd.value))
	if err != nil {
		return options{}, fmt.Errorf("resolve cwd: %w", err)
	}
	if _, err := os.Stat(absCWD); err != nil {
		return options{}, fmt.Errorf("stat cwd: %w", err)
	}
	modelValue := stringSetting(*model, flagWasSet(fs, "model"), providerName.modelEnv(), cfg.Model, "").Value
	profileSetting := stringSetting(*profile, flagWasSet(fs, "profile"), "MEMAX_CODE_PROFILE", cfg.Profile, "")
	effortSetting := stringSetting(*effort, flagWasSet(fs, "effort"), "MEMAX_CODE_EFFORT", cfg.Effort, "")
	presetSetting := stringSetting(*preset, flagWasSet(fs, "preset"), "MEMAX_CODE_PRESET", cfg.Preset, "interactive_dev")
	uiSetting := stringSetting(*uiRaw, flagWasSet(fs, "ui"), "MEMAX_CODE_UI", cfg.UI, string(renderModeAuto))
	inheritEnv, err := boolSetting(*inheritCommandEnv, flagWasSet(fs, "inherit-command-env"), "MEMAX_CODE_INHERIT_COMMAND_ENV", cfg.InheritCommandEnv, false)
	if err != nil {
		return options{}, err
	}

	opts = options{
		Prompt:            strings.TrimSpace(strings.Join(fs.Args(), " ")),
		CWD:               absCWD,
		Provider:          providerName,
		Model:             strings.TrimSpace(modelValue),
		Profile:           strings.TrimSpace(profileSetting.Value),
		Effort:            strings.TrimSpace(effortSetting.Value),
		Preset:            strings.TrimSpace(presetSetting.Value),
		ConfigPath:        configPath,
		ConfigLoaded:      configLoaded,
		SessionDir:        resolvedSessionDir,
		ResumeSessionID:   strings.TrimSpace(*resumeSessionID),
		ListSessions:      *listSessionsFlag,
		InspectTools:      *inspectTools,
		DryRun:            *dryRun,
		InheritCommandEnv: inheritEnv,
	}
	if interactive {
		if *dryRun {
			return options{}, fmt.Errorf("--interactive cannot be combined with --dry-run")
		}
		if opts.Prompt != "" {
			return options{}, fmt.Errorf("--interactive does not accept an initial prompt; type it after the shell starts")
		}
		opts.Interactive = true
	}
	if opts.Preset == "" {
		opts.Preset = "interactive_dev"
	}
	if _, err := parseModelProfile(opts.Profile); err != nil {
		if profileSetting.Source == settingSourceConfig {
			return options{}, fmt.Errorf("invalid config %s profile: %w", configPath, err)
		}
		return options{}, fmt.Errorf("unknown model profile %q (want one of: %s)", opts.Profile, validModelProfiles())
	}
	if _, err := parseModelEffort(opts.Effort); err != nil {
		if effortSetting.Source == settingSourceConfig {
			return options{}, fmt.Errorf("invalid config %s effort: %w", configPath, err)
		}
		return options{}, fmt.Errorf("unknown model effort %q (want one of: %s)", opts.Effort, validModelEfforts())
	}
	if _, err := parsePreset(opts.Preset); err != nil {
		if presetSetting.Source == settingSourceConfig {
			return options{}, fmt.Errorf("invalid config %s preset: %w", configPath, err)
		}
		return options{}, err
	}
	if opts.InspectTools {
		if opts.ResumeSessionID != "" {
			return options{}, fmt.Errorf("--inspect-tools cannot be combined with --resume")
		}
		if opts.DryRun {
			return options{}, fmt.Errorf("--inspect-tools cannot be combined with --dry-run")
		}
		if opts.Interactive {
			return options{}, fmt.Errorf("--inspect-tools cannot be combined with --interactive")
		}
		if opts.Prompt != "" {
			return options{}, fmt.Errorf("--inspect-tools does not accept a prompt")
		}
	}
	ui, err := parseRenderMode(uiSetting.Value)
	if err != nil {
		if uiSetting.Source == settingSourceConfig {
			return options{}, fmt.Errorf("invalid config %s ui: %w", configPath, err)
		}
		return options{}, err
	}
	opts.UI = ui
	return opts, nil
}

func defaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".memax-code/sessions"
	}
	return filepath.Join(home, ".memax-code", "sessions")
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".memax-code/config.json"
	}
	return filepath.Join(home, ".memax-code", "config.json")
}

type fileConfig struct {
	Provider          string `json:"provider,omitempty"`
	Model             string `json:"model,omitempty"`
	Profile           string `json:"profile,omitempty"`
	Effort            string `json:"effort,omitempty"`
	Preset            string `json:"preset,omitempty"`
	UI                string `json:"ui,omitempty"`
	SessionDir        string `json:"session_dir,omitempty"`
	InheritCommandEnv *bool  `json:"inherit_command_env,omitempty"`
}

func loadConfig(path string, explicit bool) (fileConfig, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return fileConfig{}, false, nil
		}
		return fileConfig{}, false, fmt.Errorf("open config %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fileConfig{}, false, fmt.Errorf("stat config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fileConfig{}, false, fmt.Errorf("config %s is not a regular file", path)
	}

	var cfg fileConfig
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, false, fmt.Errorf("decode config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return fileConfig{}, false, fmt.Errorf("decode config %s: %w", path, err)
		}
		return fileConfig{}, false, fmt.Errorf("decode config %s: trailing JSON value", path)
	}
	return cfg, true, nil
}

type settingSource string

const (
	settingSourceFlag     settingSource = "flag"
	settingSourceEnv      settingSource = "env"
	settingSourceConfig   settingSource = "config"
	settingSourceFallback settingSource = "fallback"
)

type stringSettingValue struct {
	Value  string
	Source settingSource
}

func stringSetting(flagValue string, flagSet bool, envKey, configValue, fallback string) stringSettingValue {
	if flagSet {
		return stringSettingValue{Value: strings.TrimSpace(flagValue), Source: settingSourceFlag}
	}
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return stringSettingValue{Value: value, Source: settingSourceEnv}
	}
	if value := strings.TrimSpace(configValue); value != "" {
		return stringSettingValue{Value: value, Source: settingSourceConfig}
	}
	return stringSettingValue{Value: fallback, Source: settingSourceFallback}
}

func boolSetting(flagValue bool, flagSet bool, envKey string, configValue *bool, fallback bool) (bool, error) {
	if flagSet {
		return flagValue, nil
	}
	if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return false, fmt.Errorf("invalid %s: %w", envKey, err)
		}
		return value, nil
	}
	if configValue != nil {
		return *configValue, nil
	}
	return fallback, nil
}

func resolvePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = home
	} else if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

type stringFlag struct {
	value string
}

func (f *stringFlag) String() string {
	return f.value
}

func (f *stringFlag) Set(value string) error {
	f.value = strings.TrimSpace(value)
	return nil
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func userFacingError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ReplaceAll(err.Error(), "coding "+"stack: ", "coding runtime: ")
	return fmt.Errorf("%s", message)
}

func defaultModel(provider provider) string {
	return strings.TrimSpace(os.Getenv(provider.modelEnv()))
}

func renderDryRun(w io.Writer, opts options) error {
	profile, err := parseModelProfile(opts.Profile)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "provider: %s\n", opts.Provider)
	fmt.Fprintf(w, "model: %s\n", valueOrUnset(opts.Model))
	fmt.Fprintf(w, "profile: %s\n", profile)
	fmt.Fprintf(w, "profile_description: %s\n", profile.Description())
	effort, err := parseModelEffort(opts.Effort)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "effort: %s\n", effort)
	fmt.Fprintf(w, "effort_description: %s\n", effort.Description())
	fmt.Fprintf(w, "preset: %s\n", opts.Preset)
	fmt.Fprintf(w, "ui: %s\n", opts.UI)
	fmt.Fprintf(w, "config: %s\n", opts.ConfigPath)
	fmt.Fprintf(w, "config_loaded: %t\n", opts.ConfigLoaded)
	fmt.Fprintf(w, "cwd: %s\n", opts.CWD)
	fmt.Fprintf(w, "session_dir: %s\n", opts.SessionDir)
	fmt.Fprintf(w, "resume_session: %s\n", valueOrUnset(opts.ResumeSessionID))
	fmt.Fprintf(w, "verification: %s\n", verificationMode(opts.CWD))
	fmt.Fprintf(w, "inherit_command_env: %t\n", opts.InheritCommandEnv)
	fmt.Fprintf(w, "prompt: %s\n", valueOrUnset(opts.Prompt))
	return nil
}

func valueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<unset>"
	}
	return value
}
