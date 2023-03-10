package describe

// the code copy and paste from https://github.com/kubernetes/kubectl/blob/master/pkg/cmd/describe/decribe.go
import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/describe"
	"k8s.io/kubectl/pkg/util/completion"
	"k8s.io/kubectl/pkg/util/i18n"
	"k8s.io/kubectl/pkg/util/templates"

	"saectl/cmd/help"
)

var (
	describeLong = templates.LongDesc(i18n.T(`
		Show details of a specific resource or group of resources.

		Print a detailed description of the selected resources, including related resources such
		as events or controllers. You may select a single object by name, all objects of that
		type, provide a name prefix, or label selector. For example:

		    $ saectl describe TYPE NAME_PREFIX

		will first check for an exact match on TYPE and NAME_PREFIX. If no such resource
		exists, it will output details for every resource that has a name prefixed with NAME_PREFIX.`))

	describeExample = templates.Examples(i18n.T(help.Wrapper(`
		# Describe a pod
		%s describe pods/nginx

		# Describe a pod identified by type and name in "pod.json"
		%s describe -f pod.json

		# Describe all pods
		%s describe pods

		# Describe pods by label name=myLabel
		%s describe po -l name=myLabel`, 4)))
)

type DescribeOptions struct {
	CmdParent string
	Selector  string
	Namespace string

	Describer  func(*meta.RESTMapping) (describe.ResourceDescriber, error)
	NewBuilder func() *resource.Builder

	BuilderArgs []string

	EnforceNamespace bool
	AllNamespaces    bool

	DescriberSettings *describe.DescriberSettings
	FilenameOptions   *resource.FilenameOptions

	genericclioptions.IOStreams
}

func NewCmdDescribe(parent string, f cmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	o := &DescribeOptions{
		FilenameOptions: &resource.FilenameOptions{},
		DescriberSettings: &describe.DescriberSettings{
			ShowEvents: true,
			ChunkSize:  cmdutil.DefaultChunkSize,
		},

		CmdParent: parent,

		IOStreams: streams,
	}

	cmd := &cobra.Command{
		Use:                   "describe (-f FILENAME | TYPE [NAME_PREFIX | -l label] | TYPE/NAME)",
		DisableFlagsInUseLine: true,
		Short:                 i18n.T("Show details of a specific resource or group of resources"),
		Long:                  describeLong + "\n\n" + cmdutil.SuggestAPIResources(parent),
		Example:               describeExample,
		ValidArgsFunction:     completion.ResourceTypeAndNameCompletionFunc(f),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(o.Complete(f, cmd, args))
			cmdutil.CheckErr(o.Validate())
			cmdutil.CheckErr(o.Run())
		},
	}
	usage := "containing the resource to describe"
	cmdutil.AddFilenameOptionFlags(cmd, o.FilenameOptions, usage)
	cmdutil.AddLabelSelectorFlagVar(cmd, &o.Selector)
	cmd.Flags().BoolVarP(&o.AllNamespaces, "all-namespaces", "A", o.AllNamespaces, "If present, list the requested object(s) across all namespaces. Namespace in current context is ignored even if specified with --namespace.")
	cmd.Flags().BoolVar(&o.DescriberSettings.ShowEvents, "show-events", o.DescriberSettings.ShowEvents, "If true, display events related to the described object.")
	cmdutil.AddChunkSizeFlag(cmd, &o.DescriberSettings.ChunkSize)
	return cmd
}

func (o *DescribeOptions) Complete(f cmdutil.Factory, cmd *cobra.Command, args []string) error {
	var err error
	o.Namespace, o.EnforceNamespace, err = f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}

	if o.AllNamespaces {
		o.EnforceNamespace = false
	}

	if len(args) == 0 && cmdutil.IsFilenameSliceEmpty(o.FilenameOptions.Filenames, o.FilenameOptions.Kustomize) {
		return fmt.Errorf("You must specify the type of resource to describe. %s\n", cmdutil.SuggestAPIResources(o.CmdParent))
	}

	o.BuilderArgs = args

	o.Describer = func(mapping *meta.RESTMapping) (describe.ResourceDescriber, error) {
		return describe.DescriberFn(f, mapping)
	}

	o.NewBuilder = f.NewBuilder

	return nil
}

func (o *DescribeOptions) Validate() error {
	return nil
}

func (o *DescribeOptions) Run() error {
	r := o.NewBuilder().
		Unstructured().
		ContinueOnError().
		NamespaceParam(o.Namespace).DefaultNamespace().AllNamespaces(o.AllNamespaces).
		FilenameParam(o.EnforceNamespace, o.FilenameOptions).
		LabelSelectorParam(o.Selector).
		ResourceTypeOrNameArgs(true, o.BuilderArgs...).
		RequestChunksOf(o.DescriberSettings.ChunkSize).
		Flatten().
		Do()
	err := r.Err()
	if err != nil {
		return err
	}

	allErrs := []error{}
	infos, err := r.Infos()
	if err != nil {
		if apierrors.IsNotFound(err) && len(o.BuilderArgs) == 2 {
			return o.DescribeMatchingResources(err, o.BuilderArgs[0], o.BuilderArgs[1])
		}
		allErrs = append(allErrs, err)
	}

	errs := sets.NewString()
	first := true
	for _, info := range infos {
		mapping := info.ResourceMapping()
		describer, err := o.Describer(mapping)
		if err != nil {
			if errs.Has(err.Error()) {
				continue
			}
			allErrs = append(allErrs, err)
			errs.Insert(err.Error())
			continue
		}
		s, err := describer.Describe(info.Namespace, info.Name, *o.DescriberSettings)
		if err != nil {
			if errs.Has(err.Error()) {
				continue
			}
			allErrs = append(allErrs, err)
			errs.Insert(err.Error())
			continue
		}
		if first {
			first = false
			fmt.Fprint(o.Out, s)
		} else {
			fmt.Fprintf(o.Out, "\n\n%s", s)
		}
	}

	if len(infos) == 0 && len(allErrs) == 0 {
		// if we wrote no output, and had no errors, be sure we output something.
		if o.AllNamespaces {
			fmt.Fprintln(o.ErrOut, "No resources found")
		} else {
			fmt.Fprintf(o.ErrOut, "No resources found in %s namespace.\n", o.Namespace)
		}
	}

	return utilerrors.NewAggregate(allErrs)
}

func (o *DescribeOptions) DescribeMatchingResources(originalError error, resource, prefix string) error {
	r := o.NewBuilder().
		Unstructured().
		NamespaceParam(o.Namespace).DefaultNamespace().
		ResourceTypeOrNameArgs(true, resource).
		SingleResourceType().
		RequestChunksOf(o.DescriberSettings.ChunkSize).
		Flatten().
		Do()
	mapping, err := r.ResourceMapping()
	if err != nil {
		return err
	}
	describer, err := o.Describer(mapping)
	if err != nil {
		return err
	}
	infos, err := r.Infos()
	if err != nil {
		return err
	}
	isFound := false
	for ix := range infos {
		info := infos[ix]
		if strings.HasPrefix(info.Name, prefix) {
			isFound = true
			s, err := describer.Describe(info.Namespace, info.Name, *o.DescriberSettings)
			if err != nil {
				return err
			}
			fmt.Fprintf(o.Out, "%s\n", s)
		}
	}
	if !isFound {
		return originalError
	}
	return nil
}
