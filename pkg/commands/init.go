package commands

import (
	"context"
	"fmt"
	"io/ioutil"
	"strconv"

	"cuelang.org/go/cue"
	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/oam-dev/kubevela/apis/types"
	"github.com/oam-dev/kubevela/pkg/application"
	cmdutil "github.com/oam-dev/kubevela/pkg/commands/util"
	"github.com/oam-dev/kubevela/pkg/plugins"
	"github.com/oam-dev/kubevela/pkg/serverlib"
	"github.com/oam-dev/kubevela/pkg/utils/env"
)

type appInitOptions struct {
	client client.Client
	cmdutil.IOStreams
	Env *types.EnvMeta

	app          *application.Application
	appName      string
	workloadName string
	workloadType string
	renderOnly   bool
}

// NewInitCommand creates `init` command
func NewInitCommand(c types.Args, ioStreams cmdutil.IOStreams) *cobra.Command {
	o := &appInitOptions{IOStreams: ioStreams}
	cmd := &cobra.Command{
		Use:                   "init",
		DisableFlagsInUseLine: true,
		Short:                 "Create scaffold for an application",
		Long:                  "Create scaffold for an application",
		Example:               "vela init",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return c.SetConfig()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			newClient, err := client.New(c.Config, client.Options{Scheme: c.Schema})
			if err != nil {
				return err
			}
			o.client = newClient
			o.Env, err = GetEnv(cmd)
			if err != nil {
				return err
			}
			o.IOStreams.Info("Welcome to use KubeVela CLI! Please describe your application.")
			o.IOStreams.Info()
			if err = o.CheckEnv(); err != nil {
				return err
			}
			if err = o.Naming(); err != nil {
				return err
			}
			if err = o.Workload(); err != nil {
				return err
			}
			if err = o.Traits(); err != nil {
				return err
			}

			if err := o.app.Validate(); err != nil {
				return err
			}

			b, err := yaml.Marshal(o.app.AppFile)
			if err != nil {
				return err
			}
			err = ioutil.WriteFile("./vela.yaml", b, 0600)
			if err != nil {
				return err
			}
			o.IOStreams.Info("\nDeployment config is rendered and written to " + color.New(color.FgCyan).Sprint("vela.yaml"))

			if o.renderOnly {
				return nil
			}

			ctx := context.Background()
			return o.app.BuildRun(ctx, o.client, o.Env, ioStreams)
		},
		Annotations: map[string]string{
			types.TagCommandType: types.TypeStart,
		},
	}
	cmd.Flags().BoolVar(&o.renderOnly, "render-only", false, "Rendering vela.yaml in current dir and do not deploy")
	cmd.SetOut(ioStreams.Out)
	return cmd
}

// Naming asks user to input app name
func (o *appInitOptions) Naming() error {
	prompt := &survey.Input{
		Message: "What would you like to name your application (required): ",
	}
	err := survey.AskOne(prompt, &o.appName, survey.WithValidator(survey.Required))
	if err != nil {
		return fmt.Errorf("read app name err %w", err)
	}
	return nil
}

// CheckEnv checks environment, e.g., domain and email.
func (o *appInitOptions) CheckEnv() error {
	if o.Env.Namespace == "" {
		o.Env.Namespace = "default"
	}
	o.Infof("Environment: %s, namespace: %s\n\n", o.Env.Name, o.Env.Namespace)
	if o.Env.Domain == "" {
		prompt := &survey.Input{
			Message: "What is the domain of your application service (optional): ",
		}
		err := survey.AskOne(prompt, &o.Env.Domain)
		if err != nil {
			return fmt.Errorf("read domain err %w", err)
		}
	}
	if o.Env.Email == "" {
		prompt := &survey.Input{
			Message: "What is your email (optional, used to generate certification): ",
		}
		err := survey.AskOne(prompt, &o.Env.Email)
		if err != nil {
			return fmt.Errorf("read email err %w", err)
		}
	}
	if _, err := env.CreateOrUpdateEnv(context.Background(), o.client, o.Env.Name, o.Env); err != nil {
		return err
	}
	return nil
}

// Workload asks user to choose workload type from installed workloads
func (o *appInitOptions) Workload() error {
	workloads, err := plugins.LoadInstalledCapabilityWithType(types.TypeWorkload)
	if err != nil {
		return err
	}
	var workloadList []string
	for _, w := range workloads {
		workloadList = append(workloadList, w.Name)
	}
	prompt := &survey.Select{
		Message: "Choose the workload type for your application (required, e.g., webservice): ",
		Options: workloadList,
	}
	err = survey.AskOne(prompt, &o.workloadType, survey.WithValidator(survey.Required))
	if err != nil {
		return fmt.Errorf("read workload type err %w", err)
	}
	workload, err := GetCapabilityByName(o.workloadType, workloads)
	if err != nil {
		return err
	}
	namePrompt := &survey.Input{
		Message: fmt.Sprintf("What would you like to name this %s (required): ", o.workloadType),
	}
	err = survey.AskOne(namePrompt, &o.workloadName, survey.WithValidator(survey.Required))
	if err != nil {
		return fmt.Errorf("read workload name err %w", err)
	}
	fs := pflag.NewFlagSet("workload", pflag.ContinueOnError)
	for _, pp := range workload.Parameters {
		p := pp
		if p.Name == "name" {
			continue
		}
		usage := p.Usage
		if usage == "" {
			usage = "what would you configure for parameter '" + color.New(color.FgCyan).Sprintf("%s", p.Name) + "'"
		}
		if p.Required {
			usage += " (required): "
		} else {
			defaultValue := fmt.Sprintf("%v", p.Default)
			if defaultValue != "" {
				usage += fmt.Sprintf(" (optional, default is %s): ", defaultValue)
			} else {
				usage += " (optional): "
			}
		}
		// nolint:exhaustive
		switch p.Type {
		case cue.StringKind:
			var data string
			prompt := &survey.Input{
				Message: usage,
			}
			var opts []survey.AskOpt
			if p.Required {
				opts = append(opts, survey.WithValidator(survey.Required))
			}
			err = survey.AskOne(prompt, &data, opts...)
			if err != nil {
				return fmt.Errorf("read param %s err %w", p.Name, err)
			}
			fs.String(p.Name, data, p.Usage)
		case cue.NumberKind, cue.FloatKind:
			var data string
			prompt := &survey.Input{
				Message: usage,
			}
			var opts []survey.AskOpt
			if p.Required {
				opts = append(opts, survey.WithValidator(survey.Required))
			}
			opts = append(opts, survey.WithValidator(func(ans interface{}) error {
				data := ans.(string)
				if data == "" && !p.Required {
					return nil
				}
				_, err := strconv.ParseFloat(data, 64)
				return err
			}))
			err = survey.AskOne(prompt, &data, opts...)
			if err != nil {
				return fmt.Errorf("read param %s err %w", p.Name, err)
			}
			val, _ := strconv.ParseFloat(data, 64)
			fs.Float64(p.Name, val, p.Usage)
		case cue.IntKind:
			var data string
			prompt := &survey.Input{
				Message: usage,
			}
			var opts []survey.AskOpt
			if p.Required {
				opts = append(opts, survey.WithValidator(survey.Required))
			}
			opts = append(opts, survey.WithValidator(func(ans interface{}) error {
				data := ans.(string)
				if data == "" && !p.Required {
					return nil
				}
				_, err := strconv.ParseInt(data, 10, 64)
				return err
			}))
			err = survey.AskOne(prompt, &data, opts...)
			if err != nil {
				return fmt.Errorf("read param %s err %w", p.Name, err)
			}
			val, _ := strconv.ParseInt(data, 10, 64)
			fs.Int64(p.Name, val, p.Usage)
		case cue.BoolKind:
			var data bool
			prompt := &survey.Confirm{
				Message: usage,
			}
			if p.Required {
				err = survey.AskOne(prompt, &data, survey.WithValidator(survey.Required))
			} else {
				err = survey.AskOne(prompt, &data)
			}
			if err != nil {
				return fmt.Errorf("read param %s err %w", p.Name, err)
			}
			fs.Bool(p.Name, data, p.Usage)
		default:
			// other type not supported
		}
	}
	o.app, err = serverlib.BaseComplete(o.Env.Name, o.workloadName, o.appName, fs, o.workloadType)
	return err
}

// GetCapabilityByName get eponymous types.Capability from workloads by name
func GetCapabilityByName(name string, workloads []types.Capability) (types.Capability, error) {
	for _, v := range workloads {
		if v.Name == name {
			return v, nil
		}
	}
	return types.Capability{}, fmt.Errorf("%s not found", name)
}

// Traits attaches specific trait to service
func (o *appInitOptions) Traits() error {
	traits, err := plugins.LoadInstalledCapabilityWithType(types.TypeTrait)
	if err != nil {
		return err
	}
	switch o.workloadType {
	case "webservice":
		// TODO(wonderflow) this should get from workload definition to know which trait should be suggestions
		var suggestTraits = []string{}
		if o.Env.Domain != "" {
			suggestTraits = append(suggestTraits, "route")
		}
		for _, tr := range suggestTraits {
			trait, err := GetCapabilityByName(tr, traits)
			if err != nil {
				continue
			}
			tflags := pflag.NewFlagSet("trait", pflag.ContinueOnError)
			for _, pa := range trait.Parameters {
				types.SetFlagBy(tflags, pa)
			}
			// TODO(wonderflow): give a way to add parameter for trait
			o.app, err = serverlib.AddOrUpdateTrait(o.Env, o.appName, o.workloadName, tflags, trait)
			if err != nil {
				return err
			}
		}
	default:
	}
	return nil
}
