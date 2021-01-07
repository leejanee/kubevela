package appfile

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/oam-dev/kubevela/apis/types"

	"github.com/oam-dev/kubevela/pkg/appfile/config"

	"github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/ghodss/yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/pkg/appfile/template"
	cmdutil "github.com/oam-dev/kubevela/pkg/commands/util"
	"github.com/oam-dev/kubevela/pkg/oam"
)

// error msg used in Appfile
var (
	ErrImageNotDefined = errors.New("image not defined")
)

// DefaultAppfilePath defines the default file path that used by `vela up` command
const (
	DefaultJSONAppfilePath         = "./vela.json"
	DefaultAppfilePath             = "./vela.yaml"
	DefaultUnknowFormatAppfilePath = "./Appfile"
)

// AppFile defines the spec of KubeVela Appfile
type AppFile struct {
	Name       string             `json:"name"`
	CreateTime time.Time          `json:"createTime,omitempty"`
	UpdateTime time.Time          `json:"updateTime,omitempty"`
	Services   map[string]Service `json:"services"`
	Secrets    map[string]string  `json:"secrets,omitempty"`

	configGetter config.Store
}

// NewAppFile init an empty AppFile struct
func NewAppFile() *AppFile {
	return &AppFile{
		Services:     make(map[string]Service),
		Secrets:      make(map[string]string),
		configGetter: config.Local{},
	}
}

// Load will load appfile from default path
func Load() (*AppFile, error) {
	if _, err := os.Stat(DefaultAppfilePath); err == nil {
		return LoadFromFile(DefaultAppfilePath)
	}
	if _, err := os.Stat(DefaultJSONAppfilePath); err == nil {
		return LoadFromFile(DefaultJSONAppfilePath)
	}
	return LoadFromFile(DefaultUnknowFormatAppfilePath)
}

// JSONToYaml will convert JSON format appfile to yaml and load the AppFile struct
func JSONToYaml(data []byte, appFile *AppFile) (*AppFile, error) {
	j, e := yaml.JSONToYAML(data)
	if e != nil {
		return nil, e
	}
	err := yaml.Unmarshal(j, appFile)
	if err != nil {
		return nil, err
	}
	return appFile, nil
}

// LoadFromFile will read the file and load the AppFile struct
func LoadFromFile(filename string) (*AppFile, error) {
	b, err := ioutil.ReadFile(filepath.Clean(filename))
	if err != nil {
		return nil, err
	}
	af := NewAppFile()
	// Add JSON format appfile support
	ext := filepath.Ext(filename)
	switch ext {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(b, af)
	case ".json":
		af, err = JSONToYaml(b, af)
	default:
		if json.Valid(b) {
			af, err = JSONToYaml(b, af)
		} else {
			err = yaml.Unmarshal(b, af)
		}
	}
	if err != nil {
		return nil, err
	}
	return af, nil
}

// BuildOAMApplication renders Appfile into Application, Scopes and other K8s Resources.
func (app *AppFile) BuildOAMApplication(env *types.EnvMeta, io cmdutil.IOStreams, tm template.Manager, silence bool) (*v1alpha2.Application, []oam.Object, error) {
	// assistantObjects currently include OAM Scope Custom Resources and ConfigMaps
	var assistantObjects []oam.Object
	servApp := new(v1alpha2.Application)
	servApp.SetNamespace(env.Namespace)
	servApp.SetName(app.Name)
	servApp.Spec.Components = []v1alpha2.ApplicationComponent{}
	for serviceName, svc := range app.GetServices() {
		if !silence {
			io.Infof("\nRendering configs for service (%s)...\n", serviceName)
		}
		configname := svc.GetUserConfigName()
		if configname != "" {
			configData, err := app.configGetter.GetConfigData(configname, env.Name)
			if err != nil {
				return nil, nil, err
			}
			decodedData, err := config.DecodeConfigFormat(configData)
			if err != nil {
				return nil, nil, err
			}
			cm, err := config.ToConfigMap(app.configGetter, config.GenConfigMapName(app.Name, serviceName, configname), env.Name, decodedData)
			if err != nil {
				return nil, nil, err
			}
			assistantObjects = append(assistantObjects, cm)
		}
		comp, err := svc.RenderServiceToApplicationComponent(tm, serviceName)
		if err != nil {
			return nil, nil, err
		}
		servApp.Spec.Components = append(servApp.Spec.Components, comp)
	}
	servApp.SetGroupVersionKind(v1alpha2.SchemeGroupVersion.WithKind("Application"))
	assistantObjects = append(assistantObjects, addDefaultHealthScopeToApplication(servApp))
	return servApp, assistantObjects, nil
}

func addWorkloadTypeLabel(comps []*v1alpha2.Component, services map[string]Service) {
	for _, comp := range comps {
		workloadType := services[comp.Name].GetType()
		workloadObject := comp.Spec.Workload.Object.(*unstructured.Unstructured)
		labels := workloadObject.GetLabels()
		if labels == nil {
			labels = map[string]string{oam.WorkloadTypeLabel: workloadType}
		} else {
			labels[oam.WorkloadTypeLabel] = workloadType
		}
		workloadObject.SetLabels(labels)
	}
}

func addDefaultHealthScopeToApplication(app *v1alpha2.Application) *v1alpha2.HealthScope {
	health := &v1alpha2.HealthScope{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha2.HealthScopeGroupVersionKind.GroupVersion().String(),
			Kind:       v1alpha2.HealthScopeKind,
		},
	}
	health.Name = FormatDefaultHealthScopeName(app.Name)
	health.Namespace = app.Namespace
	health.Spec.WorkloadReferences = make([]v1alpha1.TypedReference, 0)
	for i := range app.Spec.Components {
		// FIXME(wonderflow): the hardcode health scope should be fixed.
		data, _ := json.Marshal(map[string]string{"healthscopes.core.oam.dev": health.Name})
		app.Spec.Components[i].Scopes = runtime.RawExtension{Raw: data}
	}
	return health
}

// FormatDefaultHealthScopeName will create a default health scope name.
func FormatDefaultHealthScopeName(appName string) string {
	return appName + "-default-health"
}
