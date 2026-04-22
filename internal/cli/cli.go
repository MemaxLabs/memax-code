package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Run parses CLI arguments, builds the coding runtime, and executes one prompt.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
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
	if opts.Prompt == "" && !opts.DryRun {
		return fmt.Errorf("prompt is required unless --dry-run or --list-sessions is set")
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
	Preset            string
	UI                renderMode
	SessionDir        string
	ResumeSessionID   string
	ListSessions      bool
	ShowSessionID     string
	InspectTools      bool
	DryRun            bool
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
	providerRaw := fs.String("provider", envDefault("MEMAX_CODE_PROVIDER", string(providerOpenAI)), "model provider: openai or anthropic")
	model := fs.String("model", "", "provider model name; defaults to OPENAI_MODEL or ANTHROPIC_MODEL")
	profile := fs.String("profile", "", "coding model profile: fast, balanced, or deep")
	preset := fs.String("preset", "interactive_dev", "coding preset: safe_local, ci_repair, or interactive_dev")
	uiRaw := fs.String("ui", string(renderModeAuto), "event renderer: auto, tui, or plain")
	sessionDir := fs.String("session-dir", defaultSessionDir(), "directory for JSONL session transcripts")
	resumeSessionID := fs.String("resume", "", "resume an existing session id, or latest")
	listSessionsFlag := fs.Bool("list-sessions", false, "list saved sessions and exit")
	showSessionID := fs.String("show-session", "", "print a saved session transcript and exit")
	inspectTools := fs.Bool("inspect-tools", false, "print the model-facing tool contract and exit")
	fs.Var(cwd, "C", "alias for --cwd")
	fs.Var(cwd, "cd", "alias for --cwd")
	fs.Var(cwd, "cwd", "workspace root")
	dryRun := fs.Bool("dry-run", false, "print resolved configuration without calling a provider")
	inheritCommandEnv := fs.Bool("inherit-command-env", false, "let command tools inherit the host process environment")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: memax-code [flags] PROMPT\n")
		fmt.Fprintf(fs.Output(), "       memax-code --resume SESSION_ID|latest [flags] PROMPT\n")
		fmt.Fprintf(fs.Output(), "       memax-code --list-sessions [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --show-session SESSION_ID|latest [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --inspect-tools [flags]\n")
		fmt.Fprintf(fs.Output(), "       memax-code --dry-run [flags] [PROMPT]\n\n")
		fmt.Fprintf(fs.Output(), "Flags must precede PROMPT because Go flag parsing stops at the first positional argument.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	resolvedSessionDir, err := resolvePath(*sessionDir)
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
		return options{
			SessionDir:   resolvedSessionDir,
			ListSessions: true,
		}, nil
	}

	providerName, err := parseProvider(*providerRaw)
	if err != nil {
		if !flagWasSet(fs, "provider") && strings.TrimSpace(os.Getenv("MEMAX_CODE_PROVIDER")) != "" {
			return options{}, fmt.Errorf("invalid MEMAX_CODE_PROVIDER: %w", err)
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
	if *model == "" {
		*model = defaultModel(providerName)
	}

	opts = options{
		Prompt:            strings.TrimSpace(strings.Join(fs.Args(), " ")),
		CWD:               absCWD,
		Provider:          providerName,
		Model:             strings.TrimSpace(*model),
		Profile:           strings.TrimSpace(*profile),
		Preset:            strings.TrimSpace(*preset),
		SessionDir:        resolvedSessionDir,
		ResumeSessionID:   strings.TrimSpace(*resumeSessionID),
		ListSessions:      *listSessionsFlag,
		InspectTools:      *inspectTools,
		DryRun:            *dryRun,
		InheritCommandEnv: *inheritCommandEnv,
	}
	if opts.Preset == "" {
		opts.Preset = "interactive_dev"
	}
	if _, err := parseModelProfile(opts.Profile); err != nil {
		return options{}, fmt.Errorf("unknown model profile %q (want one of: %s)", opts.Profile, validModelProfiles())
	}
	if _, err := parsePreset(opts.Preset); err != nil {
		return options{}, err
	}
	if opts.InspectTools {
		if opts.ResumeSessionID != "" {
			return options{}, fmt.Errorf("--inspect-tools cannot be combined with --resume")
		}
		if opts.DryRun {
			return options{}, fmt.Errorf("--inspect-tools cannot be combined with --dry-run")
		}
		if opts.Prompt != "" {
			return options{}, fmt.Errorf("--inspect-tools does not accept a prompt")
		}
	}
	ui, err := parseRenderMode(*uiRaw)
	if err != nil {
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
	fmt.Fprintf(w, "preset: %s\n", opts.Preset)
	fmt.Fprintf(w, "ui: %s\n", opts.UI)
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
