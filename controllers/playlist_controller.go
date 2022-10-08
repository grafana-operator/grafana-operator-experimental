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
	"encoding/json"
	"strings"

	"github.com/go-logr/logr"
	client2 "github.com/grafana-operator/grafana-operator-experimental/controllers/client"
	gapi "github.com/grafana/grafana-api-golang-client"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	grafanav1beta1 "github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
)

// GrafanaPlayListReconciler reconciles a GrafanaPlaylist object
type GrafanaPlayListReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=GrafanaPlaylists,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=GrafanaPlaylists/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=grafana.integreatly.org,resources=GrafanaPlaylists/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GrafanaPlaylist object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *GrafanaPlayListReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := log.FromContext(ctx)
	r.Log = controllerLog

	playList := &grafanav1beta1.GrafanaPlayList{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: req.Namespace,
		Name:      req.Name,
	}, playList)

	if err != nil {
		if errors.IsNotFound(err) {
			err = r.onPlayListDeleted(ctx, req.Namespace, req.Name)
			if err != nil {
				return ctrl.Result{RequeueAfter: RequeueDelayError}, err
			}
			return ctrl.Result{}, nil
		}
		controllerLog.Error(err, "error getting grafana playList cr")
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	if playList.Spec.InstanceSelector == nil {
		controllerLog.Info("no instance selector found for playList, nothing to do", "name", playList.Name, "namespace", playList.Namespace)
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	instances, err := GetMatchingInstances(ctx, r.Client, playList.Spec.InstanceSelector)
	if err != nil {
		controllerLog.Error(err, "could not find matching instance", "name", playList.Name)
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	if len(instances.Items) == 0 {
		controllerLog.Info("no matching instances found for playList", "playList", playList.Name, "namespace", playList.Namespace)
	}

	controllerLog.Info("found matching Grafana instances for playList", "count", len(instances.Items))

	for _, grafana := range instances.Items {
		// an admin url is required to interact with grafana
		// the instance or route might not yet be ready
		if grafana.Status.AdminUrl == "" || grafana.Status.Stage != grafanav1beta1.OperatorStageComplete || grafana.Status.StageStatus != grafanav1beta1.OperatorStageResultSuccess {
			controllerLog.Info("grafana instance not ready", "grafana", grafana.Name)
			continue
		}

		// then import the playList into the matching grafana instances
		err = r.onPlayListCreated(ctx, &grafana, playList)
		if err != nil {
			controllerLog.Error(err, "error reconciling dashboard", "playList", playList.Name, "grafana", grafana.Name)
		}
	}

	return ctrl.Result{RequeueAfter: RequeueDelaySuccess}, nil

}

func (r *GrafanaPlayListReconciler) onPlayListDeleted(ctx context.Context, namespace string, name string) error {
	list := grafanav1beta1.GrafanaList{}
	opts := []client.ListOption{}
	err := r.Client.List(ctx, &list, opts...)
	if err != nil {
		return err
	}

	for _, grafana := range list.Items {
		if found, uid := grafana.FindPlayListByNamespaceAndName(namespace, name); found {
			grafanaClient, err := client2.NewGrafanaClient(ctx, r.Client, &grafana)
			if err != nil {
				return err
			}

			playList, err := grafanaClient.Playlist(uid)
			if err != nil {
				return err
			}

			err = grafanaClient.DeletePlaylist(playList.UID)
			if err != nil {
				if !strings.Contains(err.Error(), "status: 404") {
					return err
				}
			}

			err = grafana.RemovePlayList(namespace, name)
			if err != nil {
				return err
			}

			err = r.Client.Update(ctx, &grafana)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *GrafanaPlayListReconciler) onPlayListCreated(ctx context.Context, grafana *grafanav1beta1.Grafana, cr *grafanav1beta1.GrafanaPlayList) error {
	if cr.Spec.Name == "" {
		// TODO should this be nil? If they have managed to create a CR without required config it should return an error?
		return nil
	}

	grafanaClient, err := client2.NewGrafanaClient(ctx, r.Client, grafana)
	if err != nil {
		return err
	}

	id, err := r.ExistingId(grafanaClient, cr)
	if err != nil {
		return err
	}

	// always use the same uid for CR and datasource
	cr.Spec.Datasource.UID = string(cr.UID)
	datasourceBytes, err := json.Marshal(cr.Spec.Datasource)
	if err != nil {
		return err
	}

	if id == nil {
		_, err = grafanaClient.NewDataSourceFromRawData(datasourceBytes)
		// already exists error?
		if err != nil && !strings.Contains(err.Error(), "status: 409") {
			return err
		}
	} else if cr.Unchanged() == false {
		err := grafanaClient.UpdateDataSourceFromRawData(*id, datasourceBytes)
		if err != nil {
			return err
		}
	} else {
		// datasource exists and is unchanged, nothing to do
		return nil
	}

	err = r.UpdateStatus(ctx, cr)
	if err != nil {
		return err
	}

	err = grafana.AddDatasource(cr.Namespace, cr.Name, string(cr.UID))
	if err != nil {
		return err
	}

	return r.Client.Update(ctx, grafana)
	return nil
}

func (r *GrafanaPlayListReconciler) UpdateStatus(ctx context.Context, cr *grafanav1beta1.GrafanaPlayList) error {
	return nil
}

func (r *GrafanaPlayListReconciler) ExistingId(client *gapi.Client, cr *grafanav1beta1.GrafanaPlayList) (*int64, error) {

	return nil, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaPlayListReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&grafanav1beta1.GrafanaPlayList{}).
		Complete(r)
}
