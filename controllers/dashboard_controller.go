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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/banzaicloud/operator-tools/pkg/logger"
	"github.com/go-logr/logr"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/model"
	grapi "github.com/grafana/grafana-api-golang-client"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"reflect"
	"strings"

	client2 "github.com/grafana-operator/grafana-operator-experimental/controllers/client"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
)

const (
	statusCodeDashboardExistsInFolder = "412"
)

// GrafanaDashboardReconciler reconciles a GrafanaDashboard object
type GrafanaDashboardReconciler struct {
	Client    client.Client
	Log       logr.Logger
	Scheme    *runtime.Scheme
	Discovery discovery.DiscoveryInterface
}

//func NewDashboardReconciler(Client Client.Client, log logr.Logger, scheme runtime.Scheme, discovery discovery.DiscoveryInterface)  {
//	return &GrafanaDashboardReconciler{
//		Client:    Client,
//		Log:       log,
//		Scheme:    scheme,
//		Discovery: discovery,
//	}
//}

//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=grafanadashboards/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
func (r *GrafanaDashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := log.FromContext(ctx)

	dashboard := &v1beta1.GrafanaDashboard{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: req.Namespace,
		Name:      req.Name,
	}, dashboard)
	if err != nil {
		if errors.IsNotFound(err) {
			controllerLog.Info("dashboard not found, might have been deleted", "name", req.Name, req.Namespace)
			return ctrl.Result{RequeueAfter: RequeueDelayError}, nil
		}
		controllerLog.Error(err, "error getting grafana dashboard cr")
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	// skip dashboards without an instance selector
	if dashboard.Spec.InstanceSelector == nil {
		controllerLog.Info("no instance selector found for dashboard, nothing to do", "name", &dashboard.Name, "namespace", &dashboard.Namespace)
		return ctrl.Result{RequeueAfter: RequeueDelayError}, nil
	}

	instances, err := r.getMatchingInstances(ctx, dashboard.Spec.InstanceSelector)
	if err != nil {
		controllerLog.Error(err, "could not find matching instance", "name", &dashboard.Name)
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	if len(instances.Items) == 0 {
		controllerLog.Info("no matching instances found for dashboard", "dashboard", &dashboard.Name, "namespace", &dashboard.Namespace)
	}

	controllerLog.Info("found matching Grafana instances", "count", len(instances.Items))

	//complete := true

	for _, grafana := range instances.Items {
		// an admin url is required to interact with grafana
		// the instance or route might not yet be ready
		if grafana.Status.AdminUrl == "" {
			controllerLog.Info("grafana instance not ready", "grafana", grafana.Name)
			//complete = false
			continue
		}

		// first reconcile the plugins
		// append the requested dashboards to a configmap from where the
		// grafana reconciler will pick them upi
		err = r.reconcilePlugins(ctx, &grafana, dashboard)
		if err != nil {
			//complete = false
			controllerLog.Error(err, "error reconciling plugins", "dashboard", dashboard.Name, "grafana", grafana.Name)
		}

		// then import the dashboard into the matching grafana instances
		err = r.reconcileDashboardInInstance(ctx, &grafana, dashboard)
		if err != nil {
			//complete = false
			controllerLog.Error(err, "error reconciling dashboard", "dashboard", dashboard.Name, "grafana", grafana.Name)
		}
	}

	// another reconcile needed?
	//if complete {
	//	return ctrl.Result{}, nil
	//}

	return ctrl.Result{RequeueAfter: RequeueDelaySuccess}, nil
}

func (r *GrafanaDashboardReconciler) reconcileDashboardInInstance(ctx context.Context, grafana *v1beta1.Grafana, cr *v1beta1.GrafanaDashboard) error {
	if strings.TrimSpace(cr.Spec.Json) == "" {
		return nil
	}

	var dashboardFromJson map[string]interface{}

	err := json.Unmarshal([]byte(cr.Spec.Json), &dashboardFromJson)
	if err != nil {
		return err
	}

	grafanaClient, err := client2.NewGrafanaClient(ctx, r.Client, grafana)
	if err != nil {
		r.Log.Error(err, "error establishing new api client for instance", "grafana", grafana.Name, "namespace", grafana.Namespace, "dashboard", cr.Name)
		return err
	}

	dashboards, err := grafanaClient.Dashboards()
	if err != nil {
		r.Log.Error(err, "could not get dashboards")
		return err
	}
	logger.Log.Info("found dashboards", "dashboards", dashboards)

	status := cr.Status.DeepCopy()

	getDashUIDFromStatus := func(namespacedName string) string {
		if status != nil {
			for _, instances := range status.UIDforInstance {
				for grafanaInstance, dashboardUID := range instances {
					if grafanaInstance == namespacedName {
						return dashboardUID
					}
				}
			}
		}
		return ""
	}

	grafanaNamespacedName := fmt.Sprintf("%s%s", grafana.Namespace, grafana.Name)
	dashUIDfromStatus := getDashUIDFromStatus(grafanaNamespacedName)

	// dashboard is not known try to create it
	if dashUIDfromStatus == "" {
		resp, err := grafanaClient.NewDashboard(grapi.Dashboard{
			Meta: grapi.DashboardMeta{
				IsStarred: false,
				Slug:      cr.ObjectMeta.Name,
				//Folder:    ,
				//URL:       "",
			},
			Model: dashboardFromJson,
			//Folder:    0,
			Overwrite: false,
			Message:   "",
		})

		if resp != nil || err != nil {
			if !strings.Contains(fmt.Sprintf("%s", err), statusCodeDashboardExistsInFolder) {
				return err
			}
		}
	}

	for _, dashboard := range dashboards {
		if dashboard.Title == dashboardFromJson["title"] {
			if dashUIDfromStatus != "" && dashboard.UID == dashUIDfromStatus {
				//r.Log.Info("found dashboard in instance, veryfing state")
				dashFromClient, err := grafanaClient.DashboardByUID(dashUIDfromStatus)
				if err != nil {
					return err
				}

				dashfromClientModelJson, err := json.Marshal(dashFromClient.Model)
				if err != nil {
					r.Log.Error(err, "error marshalling grafana dashboard model from grafana instance")
					return err
				}
				if string(dashfromClientModelJson) == cr.Spec.Json {
					r.Log.Info("updating dashboard status for instance", "dashboard", dashboard.Title, "UID", dashboard.UID)
					x := make(map[string]string)
					x[grafanaNamespacedName] = dashboard.UID
					status.UIDforInstance = append(status.UIDforInstance, x)
					if err := r.reconcileDashboardStatus(cr, status); err != nil {
						r.Log.Error(err, "error while updating dashboard status")
						return err
					}
					r.Log.Info("dashboards are identical, nothing to do")
				}
			}

			if !strings.Contains(fmt.Sprintf("%s", status.UIDforInstance), dashboard.UID) {
				x := make(map[string]string)

				x[grafanaNamespacedName] = dashboard.UID
				status.UIDforInstance = append(status.UIDforInstance, x)
				if err := r.reconcileDashboardStatus(cr, status); err != nil {
					r.Log.Error(err, "error while updating dashboard status")
					return err
				}
			}
		}
	}
	return nil
}

func (r *GrafanaDashboardReconciler) reconcileDashboardStatus(cr *v1beta1.GrafanaDashboard, nextStatus *v1beta1.GrafanaDashboardStatus) error {
	//r.Log.Info("updating dashboard", "name", cr.ObjectMeta.Name, "namespace", cr.ObjectMeta.Namespace)
	if !reflect.DeepEqual(&cr.Status, nextStatus) {
		nextStatus.DeepCopyInto(&cr.Status)
		err := r.Client.Status().Update(context.Background(), cr)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *GrafanaDashboardReconciler) reconcilePlugins(ctx context.Context, grafana *v1beta1.Grafana, dashboard *v1beta1.GrafanaDashboard) error {
	if dashboard.Spec.Plugins == nil || len(dashboard.Spec.Plugins) == 0 {
		return nil
	}

	pluginsConfigMap := model.GetPluginsConfigMap(grafana, r.Scheme)
	selector := client.ObjectKey{
		Namespace: pluginsConfigMap.Namespace,
		Name:      pluginsConfigMap.Name,
	}

	err := r.Client.Get(ctx, selector, pluginsConfigMap)
	if err != nil {
		return err
	}

	val, err := json.Marshal(dashboard.Spec.Plugins.Sanitize())
	if err != nil {
		return err
	}

	if pluginsConfigMap.BinaryData == nil {
		pluginsConfigMap.BinaryData = make(map[string][]byte)
	}

	if bytes.Compare(val, pluginsConfigMap.BinaryData[dashboard.Name]) != 0 {
		pluginsConfigMap.BinaryData[dashboard.Name] = val
		return r.Client.Update(ctx, pluginsConfigMap)
	}

	return nil
}

func (r *GrafanaDashboardReconciler) getMatchingInstances(ctx context.Context, labelSelector *v1.LabelSelector) (v1beta1.GrafanaList, error) {
	var list v1beta1.GrafanaList
	opts := []client.ListOption{
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	err := r.Client.List(ctx, &list, opts...)
	return list, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.GrafanaDashboard{}).
		Complete(r)
}

//TODO
func (r *GrafanaDashboardReconciler) createDashboard(client *grapi.Client,
	cr *v1beta1.GrafanaDashboard) error {
	return nil
}

//TODO
func (r *GrafanaDashboardReconciler) deleteDashboard() error {
	return nil
}

//TODO
func (r *GrafanaDashboardReconciler) updateDashboard() error {
	return nil
}

//TODO
//func verifyClientAndClusterDashboardState(clientDashboards []grapi.Dashboard, clusterDashboards []grafanav1beta1.GrafanaDashboard) ([]*grafanav1beta1.GrafanaDashboard, error) {
//	dashboardsToUpdate := []grafanav1beta1.GrafanaDashboard{}
//
//	//TOOD refactor me
//	if len(clientDashboards) > 0 {
//		for _, clientDashboard := range clientDashboards {
//			fmt.Printf("%s", clientDashboard.Meta.Slug)
//		}
//	}
//	return dashboardsToUpdate, nil
//
//}
