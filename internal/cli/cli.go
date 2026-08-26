package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	ExitSuccess               = 0
	ExitUsage                 = 2
	ExitMissingDependency     = 10
	ExitAuthIncomplete        = 11
	ExitTencentUnavailable    = 12
	ExitProjectNotInitialized = 13
	ExitUnsupportedUpstream   = 14
	ExitReleaseUnavailable    = 15
	ExitIntegrityFailure      = 20
	ExitPartialResult         = 30
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

type Options struct {
	Version            string
	In                 io.Reader
	Out                io.Writer
	Err                io.Writer
	Setup              func(path string) error
	Test               func(jsonOutput bool) error
	TestOutput         func(jsonOutput bool) (string, error)
	Status             func(jsonOutput bool) error
	StatusOutput       func(jsonOutput bool) (string, error)
	Doctor             func(jsonOutput bool) error
	DoctorOutput       func(jsonOutput bool) (string, error)
	Repair             func() error
	Backup             func(destination string) error
	Restore            func(archive string) error
	RestoreWithOptions func(archive string, replaceExisting bool) error
	Install            func() (string, error)
	Update             func() (string, error)
	SetCredential      func(provider string) error
	Init               map[string]func() error
	InitNotice         map[string]string
	InitNoticeFunc     map[string]func() string
	Hook               func(client, event string, input io.Reader, output io.Writer) error
}

func (o *Options) normalize() {
	if o.In == nil {
		o.In = os.Stdin
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Err == nil {
		o.Err = os.Stderr
	}
}

func New(options Options) *cobra.Command {
	options.normalize()
	root := &cobra.Command{
		Use:           "baron",
		Short:         "Baron Nexus project continuity sidecar",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	if options.Version != "" {
		root.Version = options.Version
		root.SetVersionTemplate("baron {{.Version}}\n")
	}
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)

	root.AddCommand(initCommand("deepseek-harness", options, "Install or verify the mandatory DeepSeek Harness baseline."))
	root.AddCommand(initCommand("codex-cli", options, "Install or verify Codex CLI and Baron hooks."))
	root.AddCommand(initCommand("tencent-memory", options, "Initialize or verify TencentDB Agent Memory and Baron identity."))

	root.AddCommand(readinessCommand("test", "Read-only Baron readiness check.", options.Test, options.TestOutput, options.Out))

	setup := &cobra.Command{
		Use:   "setup [absolute-path]",
		Short: "Initialize or repair the current Baron project.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("setup accepts at most one path")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
				if !filepath.IsAbs(path) {
					return errors.New("setup path must be an existing absolute path")
				}
			}
			if options.Setup != nil {
				if err := options.Setup(path); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Setup complete.")
			return nil
		},
	}
	root.AddCommand(setup)
	root.AddCommand(readinessCommand("status", "Show the current project and system summary.", options.Status, options.StatusOutput, options.Out))
	root.AddCommand(readinessCommand("doctor", "Run deep read-only Baron diagnostics.", options.Doctor, options.DoctorOutput, options.Out))

	repair := &cobra.Command{
		Use:   "repair",
		Short: "Repair Baron-owned configuration and integrations.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.Repair != nil {
				if err := options.Repair(); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Repair complete.")
			return nil
		},
	}
	root.AddCommand(repair)

	backup := &cobra.Command{
		Use:   "backup <destination>",
		Short: "Create and verify a portable Baron backup.",
		Args:  exactArgs(1, "backup requires a destination"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.Backup != nil {
				if err := options.Backup(args[0]); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Backup complete.")
			return nil
		},
	}
	root.AddCommand(backup)

	replaceExisting := false
	restore := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Validate and restore a Baron backup.",
		Args:  exactArgs(1, "restore requires an archive"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.RestoreWithOptions != nil {
				if err := options.RestoreWithOptions(args[0], replaceExisting); err != nil {
					return err
				}
			} else if options.Restore != nil {
				if err := options.Restore(args[0]); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Restore complete.")
			return nil
		},
	}
	restore.Flags().BoolVar(&replaceExisting, "replace-existing", false, "replace an existing Baron state only after moving it to a recoverable sibling backup")
	root.AddCommand(restore)
	root.AddCommand(binaryCommand("install", "Download and install the latest verified Baron release.", options.Install))
	root.AddCommand(binaryCommand("update", "Download and atomically update to the latest verified Baron release.", options.Update))

	deepseekCommand := &cobra.Command{
		Use:   "deepseek",
		Short: "Configure the DeepSeek provider credential.",
	}
	deepseekCommand.AddCommand(credentialRotationCommand("api_key", "Validate and save the DeepSeek API key.", "deepseek", options))
	root.AddCommand(deepseekCommand)

	credentialsCommand := &cobra.Command{
		Use:   "credentials",
		Short: "Validate and rotate provider credentials.",
	}
	credentialsSetCommand := &cobra.Command{
		Use:   "set <provider>",
		Short: "Validate and save a provider credential.",
		Args:  exactArgs(1, "credentials set requires a provider"),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := strings.ToLower(strings.TrimSpace(args[0]))
			if provider != "deepseek" {
				return &ExitError{Code: ExitUnsupportedUpstream, Err: fmt.Errorf("unsupported credential provider %q; only deepseek is supported", args[0])}
			}
			return runCredentialRotation(cmd, options, provider)
		},
	}
	credentialsCommand.AddCommand(credentialsSetCommand)
	root.AddCommand(credentialsCommand)

	hook := &cobra.Command{
		Use:    "hook <client> <event>",
		Short:  "Internal lifecycle hook entry point.",
		Hidden: true,
		Args:   exactArgs(2, "hook requires a client and event"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.Hook == nil {
				return errors.New("hook runtime is not configured")
			}
			return options.Hook(args[0], args[1], cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	root.AddCommand(hook)
	return root
}

func credentialRotationCommand(use, short, provider string, options Options) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  exactArgs(0, use+" accepts no arguments"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCredentialRotation(cmd, options, provider)
		},
	}
}

func runCredentialRotation(cmd *cobra.Command, options Options, provider string) error {
	if options.SetCredential == nil {
		return errors.New("credential rotation handler is not configured")
	}
	if err := options.SetCredential(provider); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s credential update complete.\n", provider)
	return nil
}

func binaryCommand(name, short string, handler func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  exactArgs(0, name+" accepts no arguments"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if handler == nil {
				return errors.New("Baron binary release handler is not configured")
			}
			message, err := handler()
			if err != nil {
				return err
			}
			if strings.TrimSpace(message) == "" {
				message = "Baron " + name + " complete."
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), message)
			return nil
		},
	}
}

func initCommand(name string, options Options, short string) *cobra.Command {
	parent := &cobra.Command{Use: name, Short: short}
	child := &cobra.Command{
		Use:   "init",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.Init != nil && options.Init[name] != nil {
				if err := options.Init[name](); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s initialization complete.\n", name)
			notice := options.InitNotice[name]
			if options.InitNoticeFunc != nil && options.InitNoticeFunc[name] != nil {
				notice = options.InitNoticeFunc[name]()
			}
			if notice != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ACTION REQUIRED: %s\n", notice)
			}
			return nil
		},
	}
	parent.AddCommand(child)
	return parent
}

func readinessCommand(name, short string, handler func(bool) error, output func(bool) (string, error), out io.Writer) *cobra.Command {
	jsonOutput := false
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != nil {
				text, err := output(jsonOutput)
				_, _ = io.WriteString(cmd.OutOrStdout(), text)
				if err != nil {
					return err
				}
				return nil
			}
			if handler != nil {
				if err := handler(jsonOutput); err != nil {
					return err
				}
			}
			if jsonOutput {
				return json.NewEncoder(out).Encode(map[string]string{"status": "ok", "command": name})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "All required components are ready.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "render machine-readable JSON")
	return cmd
}

func exactArgs(count int, message string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != count {
			return errors.New(message)
		}
		return nil
	}
}

func Run(args []string, options Options) int {
	options.normalize()
	cmd := New(options)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		return ExitSuccess
	}
	code := ExitUsage
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	message := strings.TrimSpace(err.Error())
	if message != "" {
		_, _ = fmt.Fprintf(options.Err, "[ERROR] %s\n", message)
	}
	return code
}
