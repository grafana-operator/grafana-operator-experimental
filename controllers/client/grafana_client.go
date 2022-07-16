package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/config"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/model"
	gapi "github.com/grafana/grafana-api-golang-client"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GrafanaClient interface {
	CreateOrUpdateDashboard(dashboard *v1beta1.GrafanaDashboard) error
	CreateFolderIfNotExists(dashboard *v1beta1.GrafanaDashboard) error
	DeleteDashboard(dashboard *v1beta1.GrafanaDashboard) error
}

type grafanaClientImpl struct {
	ctx           context.Context
	grafanaClient *gapi.Client
	kubeClient    client.Client

	instanceKey string
}

func NewGrafanaClient(ctx context.Context, c client.Client, grafana *v1beta1.Grafana) (GrafanaClient, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	var timeoutDuration time.Duration
	if grafana.Spec.Client != nil && grafana.Spec.Client.TimeoutSeconds != nil {
		timeoutDuration = time.Duration(*grafana.Spec.Client.TimeoutSeconds)
		if timeoutDuration < 0 {
			timeoutDuration = 0
		}
	} else {
		timeoutDuration = 10
	}

	credentialSecret := model.GetGrafanaAdminSecret(grafana, nil)
	selector := client.ObjectKey{
		Namespace: credentialSecret.Namespace,
		Name:      credentialSecret.Name,
	}

	err := c.Get(ctx, selector, credentialSecret)
	if err != nil {
		return nil, err
	}

	username := ""
	password := ""
	if val, ok := credentialSecret.Data[config.GrafanaAdminUserEnvVar]; ok {
		username = string(val)
	} else {
		return nil, errors.New("grafana admin secret does not contain username")
	}

	if val, ok := credentialSecret.Data[config.GrafanaAdminPasswordEnvVar]; ok {
		password = string(val)
	} else {
		return nil, errors.New("grafana admin secret does not contain password")
	}

	grafanaClient, err := gapi.New(grafana.Status.AdminUrl, gapi.Config{
		BasicAuth: url.UserPassword(username, password),
		Client: &http.Client{
			Transport: transport,
			Timeout:   time.Second * timeoutDuration,
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to setup grafana client: %w", err)
	}

	return &grafanaClientImpl{
		grafanaClient: grafanaClient,
		kubeClient:    c,
		ctx:           ctx,
		instanceKey:   grafana.DashboardStatusInstanceKey(),
	}, nil
}

func (r *grafanaClientImpl) CreateFolderIfNotExists(dashboard *v1beta1.GrafanaDashboard) error {
	folder, err := r.grafanaClient.NewFolder(dashboard.Spec.Folder.Name, dashboard.Spec.Folder.UID)
	if err != nil {
		return err
	}

	old := dashboard.Status.Instances[r.instanceKey]
	dashboard.Status.Instances[r.instanceKey] = v1beta1.GrafanaDashboardInstanceStatus{
		FolderId: folder.ID,
		Version:  old.Version,
		UID:      old.UID,
	}
	err = r.kubeClient.Status().Update(r.ctx, dashboard)
	if err != nil {
		// TODO: perhaps blow up more spectacularly when this happens?
		return err
	}

	return nil
}

func (r *grafanaClientImpl) CreateOrUpdateDashboard(dashboard *v1beta1.GrafanaDashboard) error {
	model, err := dashboard.GetContent(r.ctx)
	if err != nil {
		// TODO: error reporting
		return err
	}

	status := dashboard.Status.Instances[r.instanceKey]
	if status.UID != "" && status.Version != 0 {
		existing, err := r.grafanaClient.DashboardByUID(status.UID)
		if err != nil {
			// TODO: does a 404 trigger this?
			return err
		}
		if status.Version == existing.Model["version"] {
			// TODO: does it make sense to keep track of this?
			// return nil
		}
	}

	res, err := r.grafanaClient.NewDashboard(gapi.Dashboard{
		Overwrite: true,
		Model:     model,
		Folder:    status.FolderId,
		Message:   "Updated by Grafana Operator. ResourceVersion: " + dashboard.ResourceVersion,
	})
	log.FromContext(r.ctx).Info("dashboard put result", "res", res, "err", err)

	if err != nil {
		return err
	}

	dashboard.Status.Instances[r.instanceKey] = v1beta1.GrafanaDashboardInstanceStatus{
		FolderId: status.FolderId,
		UID:      res.UID,
		Version:  res.Version,
	}

	log.FromContext(r.ctx).Info("updating dashboard status", "dashboard.status", dashboard.Status, "status", status)

	if err = r.kubeClient.Status().Update(r.ctx, dashboard); err != nil {
		// TODO: perhaps blow up more spectacularly when this happens?
		return err
	}

	return nil
}

func (r *grafanaClientImpl) DeleteDashboard(dashboard *v1beta1.GrafanaDashboard) error {
	status := dashboard.Status.Instances[r.instanceKey]

	err := r.grafanaClient.DeleteDashboardByUID(status.UID)
	if err != nil {
		return err
	}

	delete(dashboard.Status.Instances, r.instanceKey)
	return r.kubeClient.Status().Update(r.ctx, dashboard)
}
