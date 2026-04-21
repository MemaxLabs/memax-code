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
	if opts.Prompt == "" && !opts.DryRun {
		return fmt.Errorf("prompt is required unless --dry-run is set")
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
	fs.Var(cwd, "C", "alias for --cwd")
	fs.Var(cwd, "cd", "alias for --cwd")
	fs.Var(cwd, "cwd", "workspace root")
	dryRun := fs.Bool("dry-run", false, "print resolved configuration without calling a provider")
	inheritCommandEnv := fs.Bool("inherit-command-env", false, "let command tools inherit the host process environment")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: memax-code [flags] PROMPT\n")
		fmt.Fprintf(fs.Output(), "       memax-code --dry-run [flags] [PROMPT]\n\n")
		fmt.Fprintf(fs.Output(), "Flags must precede PROMPT because Go flag parsing stops at the first positional argument.\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return options{}, err
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
	return opts, nil
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
	fmt.Fprintf(w, "cwd: %s\n", opts.CWD)
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
