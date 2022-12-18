/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/metrics"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/reconcilers"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/reconcilers/grafana"
	v1 "k8s.io/api/apps/v1"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	grafanav1beta1 "github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
)

const (
	RequeueDelaySuccess = 10 * time.Second
	RequeueDelayError   = 10 * time.Second
)

// GrafanaReconciler reconciles a Grafana object
type GrafanaReconciler struct {
	client.Client
	Log       logr.Logger
	Scheme    *runtime.Scheme
	Discovery discovery.DiscoveryInterface
}

//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanas,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanas/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanas/finalizers,verbs=update
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;create;update;delete;watch

func (r *GrafanaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := log.FromContext(ctx)

	grafana := &grafanav1beta1.Grafana{}
	err := r.Get(ctx, req.NamespacedName, grafana)
	if err != nil {
		if errors.IsNotFound(err) {
			controllerLog.Info("grafana cr has been deleted", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}

		controllerLog.Error(err, "error getting grafana cr")
		return ctrl.Result{}, err
	}

	metrics.GrafanaReconciles.WithLabelValues(grafana.Name).Inc()

	finished := true
	stages := getInstallationStages()
	nextStatus := grafana.Status.DeepCopy()
	vars := &grafanav1beta1.OperatorReconcileVars{}

	for _, stage := range stages {
		controllerLog.Info("running stage", "stage", stage)

		nextStatus.Stage = stage
		reconciler := r.getReconcilerForStage(stage)

		if reconciler == nil {
			controllerLog.Info("no reconciler known for stage", "stage", stage)
			continue
		}

		status, err := reconciler.Reconcile(ctx, grafana, nextStatus, vars, r.Scheme)
		if err != nil {
			controllerLog.Error(err, "reconciler error in stage", "stage", stage)
			nextStatus.LastMessage = err.Error()

			metrics.GrafanaFailedReconciles.WithLabelValues(grafana.Name, string(stage)).Inc()
		} else {
			nextStatus.LastMessage = ""
		}

		nextStatus.StageStatus = status

		if status != grafanav1beta1.OperatorStageResultSuccess {
			return ctrl.Result{RequeueAfter: RequeueDelayError}, nil
		}
	}

	if finished {
		controllerLog.Info("grafana installation complete")
	}

	return r.updateStatus(grafana, nextStatus)
}

func (r *GrafanaReconciler) updateStatus(cr *grafanav1beta1.Grafana, nextStatus *grafanav1beta1.GrafanaStatus) (ctrl.Result, error) {
	if !reflect.DeepEqual(&cr.Status, nextStatus) {
		nextStatus.DeepCopyInto(&cr.Status)
		err := r.Client.Status().Update(context.Background(), cr)
		if err != nil {
			return ctrl.Result{
				Requeue:      true,
				RequeueAfter: RequeueDelayError,
			}, err
		}
	}

	return ctrl.Result{
		Requeue: false,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&grafanav1beta1.Grafana{}).
		Owns(&v1.Deployment{}).
		Owns(&v12.ConfigMap{}).
		Complete(r)
}

func getInstallationStages() []grafanav1beta1.OperatorStageName {
	return []grafanav1beta1.OperatorStageName{
		grafanav1beta1.OperatorStageAdminUser,
		grafanav1beta1.OperatorStageGrafanaConfig,
		grafanav1beta1.OperatorStagePvc,
		grafanav1beta1.OperatorStageServiceAccount,
		grafanav1beta1.OperatorStageService,
		grafanav1beta1.OperatorStageIngress,
		grafanav1beta1.OperatorStagePlugins,
		grafanav1beta1.OperatorStageDeployment,
		grafanav1beta1.OperatorStageComplete,
	}
}

func (r *GrafanaReconciler) getReconcilerForStage(stage grafanav1beta1.OperatorStageName) reconcilers.OperatorGrafanaReconciler {
	switch stage {
	case grafanav1beta1.OperatorStageGrafanaConfig:
		return grafana.NewConfigReconciler(r.Client)
	case grafanav1beta1.OperatorStageAdminUser:
		return grafana.NewAdminSecretReconciler(r.Client)
	case grafanav1beta1.OperatorStagePvc:
		return grafana.NewPvcReconciler(r.Client)
	case grafanav1beta1.OperatorStageServiceAccount:
		return grafana.NewServiceAccountReconciler(r.Client)
	case grafanav1beta1.OperatorStageService:
		return grafana.NewServiceReconciler(r.Client)
	case grafanav1beta1.OperatorStageIngress:
		return grafana.NewIngressReconciler(r.Client, r.Discovery)
	case grafanav1beta1.OperatorStagePlugins:
		return grafana.NewPluginsReconciler(r.Client)
	case grafanav1beta1.OperatorStageDeployment:
		return grafana.NewDeploymentReconciler(r.Client)
	case grafanav1beta1.OperatorStageComplete:
		return grafana.NewCompleteReconciler()
	default:
		return nil
	}
}
