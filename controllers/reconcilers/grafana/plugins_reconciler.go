package grafana

import (
	"context"

	"github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/reconcilers"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PluginsReconciler struct {
	client client.Client
}

func NewPluginsReconciler(client client.Client) reconcilers.OperatorGrafanaReconciler {
	return &PluginsReconciler{
		client: client,
	}
}

func (r *PluginsReconciler) Reconcile(ctx context.Context, cr *v1beta1.Grafana, status *v1beta1.GrafanaStatus, vars *v1beta1.OperatorReconcileVars, scheme *runtime.Scheme) (v1beta1.OperatorStageStatus, error) {
	vars.Plugins = status.Plugins.String()
	return v1beta1.OperatorStageResultSuccess, nil
}
