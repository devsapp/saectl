package scale

// the code copy and paste from https://github.com/kubernetes/kubectl/blob/master/pkg/cmd/scale/scale.go
import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/scale"
	"k8s.io/kubectl/pkg/util/completion"
	"k8s.io/kubectl/pkg/util/i18n"
	"k8s.io/kubectl/pkg/util/templates"

	"saectl/cmd/help"
)

var (
	scaleLong = templates.LongDesc(i18n.T(`
		Set a new size for a deployment, replica set, replication controller, or stateful set.

		Scale also allows users to specify one or more preconditions for the scale action.

		If --current-replicas or --resource-version is specified, it is validated before the
		scale is attempted, and it is guaranteed that the precondition holds true when the
		scale is sent to the server.`))

	scaleExample = templates.Examples(i18n.T(help.Wrapper(`
		# Scale a deployment named 'demo' to 3
		%s scale --replicas=3 deployment demo

		# Scale a resource identified by type and name specified in "foo.yaml" to 3
		%s scale --replicas=3 -f foo.yaml

		# If the deployment named mysql's current size is 2, scale mysql to 3
		%s scale --current-replicas=2 --replicas=3 deployment/mysql

		# Scale multiple deployment
		%s scale --replicas=5 deployment/foo deployment/bar deployment/baz`, 4)))
)

type ScaleOptions struct {
	FilenameOptions resource.FilenameOptions
	RecordFlags     *genericclioptions.RecordFlags
	PrintFlags      *genericclioptions.PrintFlags
	PrintObj        printers.ResourcePrinterFunc

	Selector        string
	All             bool
	Replicas        int
	ResourceVersion string
	CurrentReplicas int
	Timeout         time.Duration

	Recorder                     genericclioptions.Recorder
	builder                      *resource.Builder
	namespace                    string
	enforceNamespace             bool
	args                         []string
	shortOutput                  bool
	clientSet                    kubernetes.Interface
	scaler                       scale.Scaler
	unstructuredClientForMapping func(mapping *meta.RESTMapping) (resource.RESTClient, error)
	parent                       string
	dryRunStrategy               cmdutil.DryRunStrategy
	dryRunVerifier               *resource.QueryParamVerifier

	genericclioptions.IOStreams
}

func NewScaleOptions(ioStreams genericclioptions.IOStreams) *ScaleOptions {
	return &ScaleOptions{
		PrintFlags:      genericclioptions.NewPrintFlags("scaled"),
		RecordFlags:     genericclioptions.NewRecordFlags(),
		CurrentReplicas: -1,
		Recorder:        genericclioptions.NoopRecorder{},
		IOStreams:       ioStreams,
	}
}

// NewCmdScale returns a cobra command with the appropriate configuration and flags to run scale
func NewCmdScale(f cmdutil.Factory, ioStreams genericclioptions.IOStreams) *cobra.Command {
	o := NewScaleOptions(ioStreams)

	validArgs := []string{"deployment"}

	cmd := &cobra.Command{
		Use:                   "scale [--current-replicas=count] --replicas=COUNT (-f FILENAME | TYPE NAME)",
		DisableFlagsInUseLine: true,
		Short:                 i18n.T("Set a new size for a deployment, replica set, or replication controller"),
		Long:                  scaleLong,
		Example:               scaleExample,
		ValidArgsFunction:     completion.SpecifiedResourceTypeAndNameCompletionFunc(f, validArgs),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(o.Complete(f, cmd, args))
			cmdutil.CheckErr(o.Validate())
			cmdutil.CheckErr(o.RunScale())
		},
	}

	o.PrintFlags.AddFlags(cmd)

	cmd.Flags().BoolVar(&o.All, "all", o.All, "Select all resources in the namespace of the specified resource types")
	cmd.Flags().IntVar(&o.CurrentReplicas, "current-replicas", o.CurrentReplicas, "Precondition for current size. Requires that the current size of the resource match this value in order to scale. -1 (default) for no condition.")
	cmd.Flags().IntVar(&o.Replicas, "replicas", o.Replicas, "The new desired number of replicas. Required.")
	cmd.MarkFlagRequired("replicas")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 0, "The length of time to wait before giving up on a scale operation, zero means don't wait. Any other values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	cmdutil.AddFilenameOptionFlags(cmd, &o.FilenameOptions, "identifying the resource to set a new size")
	cmdutil.AddDryRunFlag(cmd)
	return cmd
}

func (o *ScaleOptions) Complete(f cmdutil.Factory, cmd *cobra.Command, args []string) error {
	var err error
	o.RecordFlags.Complete(cmd)
	o.Recorder, err = o.RecordFlags.ToRecorder()
	if err != nil {
		return err
	}
	printer, err := o.PrintFlags.ToPrinter()
	if err != nil {
		return err
	}
	o.PrintObj = printer.PrintObj

	o.dryRunStrategy, err = cmdutil.GetDryRunStrategy(cmd)
	if err != nil {
		return err
	}
	dynamicClient, err := f.DynamicClient()
	if err != nil {
		return err
	}
	o.dryRunVerifier = resource.NewQueryParamVerifier(dynamicClient, f.OpenAPIGetter(), resource.QueryParamDryRun)

	o.namespace, o.enforceNamespace, err = f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}
	o.builder = f.NewBuilder()
	o.args = args
	o.shortOutput = cmdutil.GetFlagString(cmd, "output") == "name"
	o.clientSet, err = f.KubernetesClientSet()
	if err != nil {
		return err
	}
	o.scaler, err = scaler(f)
	if err != nil {
		return err
	}
	o.unstructuredClientForMapping = f.UnstructuredClientForMapping
	o.parent = cmd.Parent().Name()

	return nil
}

func (o *ScaleOptions) Validate() error {
	if o.Replicas < 0 {
		return fmt.Errorf("The --replicas=COUNT flag is required, and COUNT must be greater than or equal to 0")
	}

	if o.CurrentReplicas < -1 {
		return fmt.Errorf("The --current-replicas must specify an integer of -1 or greater")
	}

	return nil
}

// RunScale executes the scaling
func (o *ScaleOptions) RunScale() error {
	r := o.builder.
		Unstructured().
		ContinueOnError().
		NamespaceParam(o.namespace).DefaultNamespace().
		FilenameParam(o.enforceNamespace, &o.FilenameOptions).
		ResourceTypeOrNameArgs(o.All, o.args...).
		Flatten().
		LabelSelectorParam(o.Selector).
		Do()
	err := r.Err()
	if err != nil {
		return err
	}

	infos := []*resource.Info{}
	r.Visit(func(info *resource.Info, err error) error {
		if err == nil {
			infos = append(infos, info)
		}
		return nil
	})

	if len(o.ResourceVersion) != 0 && len(infos) > 1 {
		return fmt.Errorf("cannot use --resource-version with multiple resources")
	}

	// only set a precondition if the user has requested one.  A nil precondition means we can do a blind update, so
	// we avoid a Scale GET that may or may not succeed
	var precondition *scale.ScalePrecondition
	if o.CurrentReplicas != -1 || len(o.ResourceVersion) > 0 {
		precondition = &scale.ScalePrecondition{Size: o.CurrentReplicas, ResourceVersion: o.ResourceVersion}
	}
	retry := scale.NewRetryParams(1*time.Second, 5*time.Minute)

	var waitForReplicas *scale.RetryParams
	if o.Timeout != 0 && o.dryRunStrategy == cmdutil.DryRunNone {
		waitForReplicas = scale.NewRetryParams(1*time.Second, o.Timeout)
	}

	counter := 0
	err = r.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		counter++

		mapping := info.ResourceMapping()
		if o.dryRunStrategy == cmdutil.DryRunClient {
			return o.PrintObj(info.Object, o.Out)
		}
		if err := o.scaler.Scale(info.Namespace, info.Name, uint(o.Replicas), precondition, retry, waitForReplicas, mapping.Resource, o.dryRunStrategy == cmdutil.DryRunServer); err != nil {
			return err
		}

		// if the recorder makes a change, compute and create another patch
		if mergePatch, err := o.Recorder.MakeRecordMergePatch(info.Object); err != nil {
			klog.V(4).Infof("error recording current command: %v", err)
		} else if len(mergePatch) > 0 {
			client, err := o.unstructuredClientForMapping(mapping)
			if err != nil {
				return err
			}
			helper := resource.NewHelper(client, mapping)
			if _, err := helper.Patch(info.Namespace, info.Name, types.MergePatchType, mergePatch, nil); err != nil {
				klog.V(4).Infof("error recording reason: %v", err)
			}
		}

		return o.PrintObj(info.Object, o.Out)
	})
	if err != nil {
		return err
	}
	if counter == 0 {
		return fmt.Errorf("no objects passed to scale")
	}
	return nil
}

func scaler(f cmdutil.Factory) (scale.Scaler, error) {
	scalesGetter, err := cmdutil.ScaleClientFn(f)
	if err != nil {
		return nil, err
	}

	return scale.NewScaler(scalesGetter), nil
}
