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
	"errors"
	"fmt"
	"strings"

	client2 "github.com/grafana-operator/grafana-operator-experimental/controllers/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	grafanav1beta1 "github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
)

// GrafanaDashboardReconciler reconciles a GrafanaDashboard object
type GrafanaDashboardReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GrafanaDashboard object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *GrafanaDashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := log.FromContext(ctx).WithValues("dashboard", req.Name, "namespace", req.Namespace)

	dashboard := &grafanav1beta1.GrafanaDashboard{}
	err := r.Get(ctx, req.NamespacedName, dashboard)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			controllerLog.Info("grafana dashboard cr has been deleted", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}

		controllerLog.Error(err, "error getting grafana dashboard cr")
		return ctrl.Result{}, err
	}

	// skip dashboards without an instance selector
	if dashboard.Spec.InstanceSelector == nil {
		return ctrl.Result{}, nil
	}

	instances, err := r.getMatchingInstances(ctx, dashboard.Spec.InstanceSelector)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(instances.Items) == 0 {
		controllerLog.Info("no matching instances found for dashboard")
	}

	controllerLog.Info("found matching Grafana instances", "count", len(instances.Items))

	finalizer := "grafana.integreatly.org/dashboard-finalizer"
	if dashboard.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(dashboard, finalizer) {
			controllerutil.AddFinalizer(dashboard, finalizer)
			if err := r.Update(ctx, dashboard); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(dashboard, finalizer) {
			for _, instance := range instances.Items {
				if _, ok := dashboard.Status.Instances[instance.DashboardStatusInstanceKey()]; !ok {
					continue
				}
				if err := r.handleFinalizerForInstance(ctx, &instance, dashboard); err != nil {
					// if fail to delete the external dependency here, return with error
					// so that it can be retried
					return ctrl.Result{}, err
				}
			}
			controllerutil.RemoveFinalizer(dashboard, finalizer)
			if err := r.Update(ctx, dashboard); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	if dashboard.Spec.Folder == nil {
		dashboard.Spec.Folder = &grafanav1beta1.GrafanaDashboardFolderSpec{
			Name: dashboard.Namespace,
		}
	}

	if dashboard.Status.Instances == nil {
		dashboard.Status.Instances = map[string]grafanav1beta1.GrafanaDashboardInstanceStatus{}
	}

	// TODO: error backoff

	complete := true
	for _, grafana := range instances.Items {
		// an admin url is required to interact with grafana
		// the instance or route might not yet be ready
		if grafana.Status.AdminUrl == "" {
			controllerLog.Info("grafana instance not ready", "grafana", grafana.Name, "grafanaNamespace", grafana.Namespace)
			complete = false
			continue
		}

		// first reconcile the plugins
		// append the requested dashboards to a configmap from where the
		// grafana reconciler will pick them up
		err = r.reconcilePlugins(ctx, &grafana, dashboard)
		if err != nil {
			complete = false
			controllerLog.Error(err, "error reconciling plugins", "grafana", grafana.Name, "grafanaNamespace", grafana.Namespace)
			continue
		}

		// then import the dashboard into the matching grafana instances
		err = r.reconcileFolder(ctx, &grafana, dashboard)
		if err != nil {
			complete = false
			controllerLog.Error(err, "error reconciling folder", "grafana", grafana.Name, "grafanaNamespace", grafana.Namespace)
			continue
		}
		// then import the dashboard into the matching grafana instances
		err = r.reconcileDashboard(ctx, &grafana, dashboard)
		if err != nil {
			complete = false
			if e := errors.Unwrap(err); e != nil {
				err = e
			}
			controllerLog.Error(err, "error reconciling dashboard", "grafana", grafana.Name, "grafanaNamespace", grafana.Namespace)
			continue
		} else {
			controllerLog.Info("successfully reconciled dashboard", "grafana", grafana.Name, "grafanaNamespace", grafana.Namespace)
		}
	}

	// another reconcile needed?
	if complete {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: RequeueDelayError}, nil
}

func (r *GrafanaDashboardReconciler) reconcileDashboard(ctx context.Context, grafana *grafanav1beta1.Grafana, dashboard *grafanav1beta1.GrafanaDashboard) error {
	c, err := r.getGrafanaClient(ctx, grafana, dashboard)
	if err != nil {
		return err
	}
	llog := log.FromContext(ctx)

	if strings.TrimSpace(dashboard.Spec.Json) == "" && dashboard.Spec.GzipJson == nil && dashboard.Spec.URL == "" && dashboard.Spec.GrafanaCom == nil {
		llog.Info("missing json, url, or grafanacom id")
		return nil
	}

	return c.CreateOrUpdateDashboard(dashboard)
}

func (r *GrafanaDashboardReconciler) handleFinalizerForInstance(ctx context.Context, grafana *grafanav1beta1.Grafana, dashboard *grafanav1beta1.GrafanaDashboard) error {
	c, err := r.getGrafanaClient(ctx, grafana, dashboard)
	if err != nil {
		return err
	}
	err = c.DeleteDashboard(dashboard)
	if err != nil {
		return fmt.Errorf("Failed to delete dashboard: %w", err)
	}
	return nil
}

func (r *GrafanaDashboardReconciler) reconcileFolder(ctx context.Context, grafana *grafanav1beta1.Grafana, dashboard *grafanav1beta1.GrafanaDashboard) error {
	c, err := r.getGrafanaClient(ctx, grafana, dashboard)
	if err != nil {
		return err
	}
	return c.CreateFolderIfNotExists(dashboard)
}

func (r *GrafanaDashboardReconciler) getGrafanaClient(ctx context.Context, grafana *grafanav1beta1.Grafana, dashboard *grafanav1beta1.GrafanaDashboard) (client2.GrafanaClient, error) {
	client, err := client2.NewGrafanaClient(ctx, r.Client, grafana)
	if err != nil {
		return nil, fmt.Errorf("error setting up grafana client: %w", err)
	}
	return client, nil
}

func (r *GrafanaDashboardReconciler) reconcilePlugins(ctx context.Context, grafana *grafanav1beta1.Grafana, dashboard *grafanav1beta1.GrafanaDashboard) error {
	if dashboard.Spec.Plugins == nil || len(dashboard.Spec.Plugins) == 0 {
		return nil
	}

	plugins, err := grafana.Status.Plugins.ConsolidatedConcat(dashboard.Spec.Plugins)
	if err != nil {
		return fmt.Errorf("failed to consolidate plugin list: %w", err)
	}
	llog := log.FromContext(ctx)

	llog.Info("consolidated pluginlist", "plugins", plugins)

	grafana.Status.Plugins = plugins
	if err := r.Client.Status().Update(ctx, grafana); err != nil {
		llog.Info("failed to set plugins status", "err", err)
		return fmt.Errorf("failed to update plugin list in grafana status: %w", err)
	}

	return nil
}

func (r *GrafanaDashboardReconciler) getMatchingInstances(ctx context.Context, labelSelector *v1.LabelSelector) (grafanav1beta1.GrafanaList, error) {
	var list grafanav1beta1.GrafanaList
	opts := []client.ListOption{
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	err := r.Client.List(ctx, &list, opts...)
	return list, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&grafanav1beta1.GrafanaDashboard{}).
		Complete(r)
}
