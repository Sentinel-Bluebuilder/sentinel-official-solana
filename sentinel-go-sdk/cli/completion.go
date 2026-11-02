package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func NewCompletionCmd(shortNames ...string) *cobra.Command {
	cmd := &cobra.Command{
		Short: "generate a shell completion script",
		Use:   "completion",
	}

	cmd.AddCommand(
		newCompletionShellCmd("bash", shortNames, func(root *cobra.Command, w io.Writer, descriptions bool) error {
			return root.GenBashCompletionV2(w, descriptions)
		}),
		newCompletionShellCmd("fish", shortNames, func(root *cobra.Command, w io.Writer, descriptions bool) error {
			return root.GenFishCompletion(w, descriptions)
		}),
		newCompletionShellCmd("powershell", shortNames, func(root *cobra.Command, w io.Writer, descriptions bool) error {
			if !descriptions {
				return root.GenPowerShellCompletion(w)
			}

			return root.GenPowerShellCompletionWithDesc(w)
		}),
		newCompletionShellCmd("zsh", shortNames, func(root *cobra.Command, w io.Writer, descriptions bool) error {
			if !descriptions {
				return root.GenZshCompletionNoDesc(w)
			}

			return root.GenZshCompletion(w)
		}),
	)

	return cmd
}

func newCompletionShellCmd(
	shell string,
	shortNames []string,
	generate func(root *cobra.Command, w io.Writer, descriptions bool) error,
) *cobra.Command {
	var noDescriptions bool

	run := func(cmd *cobra.Command, _ []string) error {
		root := cmd.Root()
		names := append([]string{root.Name()}, shortNames...)

		use := root.Use
		defer func() { root.Use = use }()

		if shell == "zsh" {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "#compdef %s\n", strings.Join(names, " ")); err != nil {
				return fmt.Errorf("writing zsh completion header: %w", err)
			}
		}

		for _, name := range names {
			root.Use = name

			if err := generate(root, cmd.OutOrStdout(), !noDescriptions); err != nil {
				return fmt.Errorf("generating %s completion for %s: %w", shell, name, err)
			}
		}

		return nil
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: fmt.Sprintf("generate the %s completion script", shell),
		Use:   shell,
	}
	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "disable completion descriptions")

	return cmd
}
