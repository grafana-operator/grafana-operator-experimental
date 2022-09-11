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

	"github.com/go-logr/logr"
	gapi "github.com/grafana/grafana-api-golang-client"
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

	return ctrl.Result{RequeueAfter: RequeueDelaySuccess}, nil

}

func (r *GrafanaPlayListReconciler) onPlayListDeleted(ctx context.Context, namespace string, name string) error {
	return nil
}

func (r *GrafanaPlayListReconciler) onPlayListCreated(ctx context.Context, grafana *grafanav1beta1.Grafana, cr *grafanav1beta1.GrafanaPlayList) error {
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
