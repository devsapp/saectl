package options

import (
	"io"

	"github.com/spf13/cobra"
	"k8s.io/kubectl/pkg/util/i18n"
	"k8s.io/kubectl/pkg/util/templates"
	"saectl/cmd/help"
)

var (
	optionsExample = templates.Examples(i18n.T(help.Wrapper(`
		# Print flags inherited by all commands
		%s options`, 1)))
)

// NewCmdOptions implements the options command
func NewCmdOptions(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "options",
		Short:   i18n.T("Print the list of flags inherited by all commands"),
		Long:    i18n.T("Print the list of flags inherited by all commands"),
		Example: optionsExample,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Usage()
		},
	}

	cmd.SetOut(out)
	cmd.SetErr(out)

	templates.UseOptionsTemplates(cmd)
	return cmd
}
