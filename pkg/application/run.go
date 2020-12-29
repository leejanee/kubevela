package application

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/apis/types"
	cmdutil "github.com/oam-dev/kubevela/pkg/commands/util"
)

// BuildRun will build OAM and deploy from Appfile
func (app *Application) BuildRun(ctx context.Context, client client.Client, env *types.EnvMeta, io cmdutil.IOStreams) error {
	newApp, err := app.InitTasks(io)
	if err != nil {
		return err
	}
	servApp := newApp.Object(env.Namespace)
	existApp := new(v1alpha2.Application)
	if err := client.Get(ctx, ctypes.NamespacedName{Name: servApp.Name, Namespace: servApp.Namespace}, existApp); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	servApp.ResourceVersion = existApp.ResourceVersion
	if servApp.ResourceVersion == "" {
		return client.Create(ctx, servApp)
	}
	return client.Update(ctx, servApp)

}
