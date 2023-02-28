package version

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"saectl/cmd/help"
	"saectl/version"
)

func NewVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: help.Wrapper("Prints %s build version information", 1),
		Long:  help.Wrapper("Prints %s build version information", 1),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf(`CLI Version: %v
GitRevision: %v
GolangVersion: %v
`, version.SaeCtlVersion, version.GitRevision, runtime.Version())
		},
	}
}
