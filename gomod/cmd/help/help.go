package help

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var CommandName string
var RootCommand string

func init() {
	CommandName = "saectl"
	RootCommand = "saectl"
	if strings.HasPrefix(filepath.Base(os.Args[0]), "kubectl-") {
		CommandName = "kubectl sae"
		RootCommand = "kubectl-sae"
	}
}

func Wrapper(info string, c int) string {
	cm := make([]any, c, c)
	for i := 0; i < c; i++ {
		cm[i] = CommandName
	}
	return fmt.Sprintf(info, cm...)
}
