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
	"fmt"
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

// GrafanaPlaylistReconciler reconciles a GrafanaPlaylist object
type GrafanaPlaylistReconciler struct {
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
func (r *GrafanaPlaylistReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := log.FromContext(ctx)
	r.Log = controllerLog

	playlist := &grafanav1beta1.GrafanaPlaylist{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: req.Namespace,
		Name:      req.Name,
	}, playlist)

	if err != nil {
		if errors.IsNotFound(err) {
			err = r.onPlaylistDeleted(ctx, req.Namespace, req.Name)
			if err != nil {
				return ctrl.Result{RequeueAfter: RequeueDelayError}, err
			}
			return ctrl.Result{}, nil
		}
		controllerLog.Error(err, "error getting grafana dashboard cr")
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	if playlist.Spec.InstanceSelector == nil {
		controllerLog.Info("no instance selector found for playlist, nothing to do", "name", playlist.Name, "namespace", playlist.Namespace)
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	instances, err := GetMatchingInstances(ctx, r.Client, playlist.Spec.InstanceSelector)
	if err != nil {
		controllerLog.Error(err, "could not find matching instance", "name", playlist.Name)
		return ctrl.Result{RequeueAfter: RequeueDelayError}, err
	}

	if len(instances.Items) == 0 {
		controllerLog.Info("no matching instances found for playlist", "playlist", playlist.Name, "namespace", playlist.Namespace)
	}

	controllerLog.Info("found matching Grafana instances for playlist", "count", len(instances.Items))

	for _, grafana := range instances.Items {
		// an admin url is required to interact with grafana
		// the instance or route might not yet be ready
		if grafana.Status.AdminUrl == "" || grafana.Status.Stage != grafanav1beta1.OperatorStageComplete || grafana.Status.StageStatus != grafanav1beta1.OperatorStageResultSuccess {
			controllerLog.Info("grafana instance not ready", "grafana", grafana.Name)
			continue
		}

		// first reconcile the plugins
		// append the requested dashboards to a configmap from where the
		// grafana reconciler will pick them upi
		err = ReconcilePlugins(ctx, r.Client, r.Scheme, &grafana, playlist.Spec.Plugins, fmt.Sprintf("%v-playlist", playlist.Name))
		if err != nil {
			controllerLog.Error(err, "error reconciling plugins", "playlist", playlist.Name, "grafana", grafana.Name)
		}

		// then import the dashboard into the matching grafana instances
		err = r.onPlaylistCreated(ctx, &grafana, playlist)
		if err != nil {
			controllerLog.Error(err, "error reconciling dashboard", "playlist", playlist.Name, "grafana", grafana.Name)
		}
	}

	return ctrl.Result{RequeueAfter: RequeueDelaySuccess}, nil

}

func (r *GrafanaPlaylistReconciler) onPlaylistDeleted(ctx context.Context, namespace string, name string) error {
	list := grafanav1beta1.GrafanaList{}
	opts := []client.ListOption{}
	err := r.Client.List(ctx, &list, opts...)
	if err != nil {
		return err
	}

	for _, grafana := range list.Items {
		if found, uid := grafana.FindPlalistByNamespaceAndName(namespace, name); found {
			grafanaClient, err := client2.NewGrafanaClient(ctx, r.Client, &grafana)
			if err != nil {
				return err
			}

			playlist, err := grafanaClient.PlaylistByUID(uid)
			if err != nil {
				return err
			}

			err = grafanaClient.DeletePlaylist(playlist.ID)
			if err != nil {
				if !strings.Contains(err.Error(), "status: 404") {
					return err
				}
			}

			err = grafana.RemovePlaylist(namespace, name)
			if err != nil {
				return err
			}

			err = ReconcilePlugins(ctx, r.Client, r.Scheme, &grafana, nil, fmt.Sprintf("%v-playlist", name))
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

func (r *GrafanaPlaylistReconciler) onPlaylistCreated(ctx context.Context, grafana *grafanav1beta1.Grafana, cr *grafanav1beta1.GrafanaPlaylist) error {
	if cr.Spec.Playlist == nil {
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

	// always use the same uid for CR and playlist
	cr.Spec.Playlist.UID = string(cr.UID)
	PlaylistBytes, err := json.Marshal(cr.Spec.Playlist)
	if err != nil {
		return err
	}

	if id == nil {
		_, err = grafanaClient.NewPlaylistFromRawData(PlaylistBytes)
		// already exists error?
		if err != nil && !strings.Contains(err.Error(), "status: 409") {
			return err
		}
	} else if cr.Unchanged() == false {
		err := grafanaClient.UpdatePlaylistFromRawData(*id, playlistBytes)
		if err != nil {
			return err
		}
	} else {
		// playlist exists and is unchanged, nothing to do
		return nil
	}

	err = r.UpdateStatus(ctx, cr)
	if err != nil {
		return err
	}

	err = grafana.AddPlaylist(cr.Namespace, cr.Name, string(cr.UID))
	if err != nil {
		return err
	}

	return r.Client.Update(ctx, grafana)
}

func (r *GrafanaPlaylistReconciler) UpdateStatus(ctx context.Context, cr *grafanav1beta1.GrafanaPlaylist) error {
	cr.Status.Hash = cr.Hash()
	return r.Client.Status().Update(ctx, cr)
}

func (r *GrafanaPlaylistReconciler) ExistingId(client *gapi.Client, cr *grafanav1beta1.GrafanaPlaylist) (*int64, error) {
	playlists, err := client.Playlists()
	if err != nil {
		return nil, err
	}
	for _, playlist := range playlists {
		if playlist.UID == string(cr.UID) {
			return &playlist.ID, nil
		}
	}
	return nil, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaPlaylistReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&grafanav1beta1.GrafanaPlaylist{}).
		Complete(r)
}
