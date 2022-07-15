package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
)

type GrafanaRequest struct {
	Dashboard  json.RawMessage `json:"dashboard"`
	FolderId   int64           `json:"folderId"`
	FolderName string          `json:"folderName"`
	Overwrite  bool            `json:"overwrite"`
}

type GrafanaResponse struct {
	ID         *uint   `json:"id"`
	OrgID      *uint   `json:"orgId"`
	Message    *string `json:"message"`
	Slug       *string `json:"slug"`
	Version    *int    `json:"version"`
	Status     *string `json:"resp"`
	UID        *string `json:"uid"`
	URL        *string `json:"url"`
	FolderId   *int64  `json:"folderId"`
	FolderName string  `json:"folderName"`
}

type GrafanaClient interface {
	CreateOrUpdateDashboard(dashboard *v1beta1.GrafanaDashboard) error
	CreateFolderIfNotExists(dashboard *v1beta1.GrafanaDashboard) error
	DeleteDashboard(dashboard *v1beta1.GrafanaDashboard) error
}

type grafanaClientImpl struct {
	ctx           context.Context
	grafanaClient *gapi.Client
	kubeClient    client.Client
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
	}, nil
}

func (r *grafanaClientImpl) CreateFolderIfNotExists(dashboard *v1beta1.GrafanaDashboard) error {
	folder, err := r.grafanaClient.NewFolder(dashboard.Spec.Folder.Name, dashboard.Spec.Folder.UID)
	if err != nil {
		return err
	}

	dashboard.Status.FolderId = folder.ID
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

	if dashboard.Status.GrafanaUID != "" && dashboard.Status.GrafanaVersion != 0 {
		existing, err := r.grafanaClient.DashboardByUID(dashboard.Status.GrafanaUID)
		if err != nil {
			// TODO: does a 404 trigger this?
			return err
		}
		if dashboard.Status.GrafanaVersion == existing.Model["version"] {
			// TODO: is this optimization possible?
			// return nil
		}

	}

	res, err := r.grafanaClient.NewDashboard(gapi.Dashboard{
		Overwrite: true,
		Model:     model,
		Folder:    dashboard.Status.FolderId,
		Message:   "Updated by Grafana Operator. ResourceVersion: " + dashboard.ResourceVersion,
	})

	if err != nil {
		return err
	}

	dashboard.Status.GrafanaUID = res.UID
	dashboard.Status.GrafanaVersion = res.Version
	if err = r.kubeClient.Status().Update(r.ctx, dashboard); err != nil {
		// TODO: perhaps blow up more spectacularly when this happens?
		return err
	}

	return nil
}

func (r *grafanaClientImpl) DeleteDashboard(dashboard *v1beta1.GrafanaDashboard) error {
	return r.grafanaClient.DeleteDashboardByUID(dashboard.Status.GrafanaUID)
}
