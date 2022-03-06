package grafana

import (
	"context"

	"github.com/nissessenap/grafana-operator-experimental/api/v1beta1"
	"github.com/nissessenap/grafana-operator-experimental/controllers/config"
	"github.com/nissessenap/grafana-operator-experimental/controllers/model"
	"github.com/nissessenap/grafana-operator-experimental/controllers/reconcilers"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ConfigReconciler struct {
	client client.Client
}

func NewConfigReconciler(client client.Client) reconcilers.OperatorGrafanaReconciler {
	return &ConfigReconciler{
		client: client,
	}
}

func (r *ConfigReconciler) Reconcile(ctx context.Context, cr *v1beta1.Grafana, status *v1beta1.GrafanaStatus, vars *v1beta1.OperatorReconcileVars, scheme *runtime.Scheme) (v1beta1.OperatorStageStatus, error) {
	_ = log.FromContext(ctx)

	ini := config.NewGrafanaIni(&cr.Spec.Config)
	config, hash := ini.Write()
	vars.ConfigHash = hash

	configMap := model.GetGrafanaConfigMap(cr, scheme)
	_, err := controllerutil.CreateOrUpdate(ctx, r.client, configMap, func() error {
		if configMap.Data == nil {
			configMap.Data = make(map[string]string)
		}
		configMap.Data["grafana.ini"] = config
		return nil
	})

	if err != nil {
		return v1beta1.OperatorStageResultFailed, err
	}
	return v1beta1.OperatorStageResultSuccess, nil
}
