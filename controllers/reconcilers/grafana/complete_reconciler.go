package grafana

import (
	"context"

	"github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/reconcilers"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CompleteReconciler struct{}

func NewCompleteReconciler() reconcilers.OperatorGrafanaReconciler {
	return &CompleteReconciler{}
}

func (r *CompleteReconciler) Reconcile(ctx context.Context, cr *v1beta1.Grafana, status *v1beta1.GrafanaStatus, vars *v1beta1.OperatorReconcileVars, scheme *runtime.Scheme) (v1beta1.OperatorStageStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("grafana installation complete")
	return v1beta1.OperatorStageResultSuccess, nil
}
