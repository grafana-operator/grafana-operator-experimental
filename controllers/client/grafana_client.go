package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grafana-operator/grafana-operator-experimental/api/v1beta1"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/config"
	"github.com/grafana-operator/grafana-operator-experimental/controllers/model"
	gapi "github.com/grafana/grafana-api-golang-client"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const grafanaComDashboardApiUrlRoot = "https://grafana.com/api/dashboards"

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
	folders, err := r.grafanaClient.Folders()
	if err != nil {
		return err
	}
	var folder *gapi.Folder
	for _, f := range folders {
		if dashboard.Spec.Folder.UID != "" {
			if dashboard.Spec.Folder.UID == f.UID {
				folder = &f
				break
			}
		} else {
			if dashboard.Spec.Folder.Name == f.Title {
				folder = &f
				break
			}
		}
	}

	if folder == nil {
		f, err := r.grafanaClient.NewFolder(dashboard.Spec.Folder.Name, dashboard.Spec.Folder.UID)
		if err != nil {
			return err
		}
		folder = &f
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
	model, err := r.getDashboardContent(dashboard)
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
		if float64(status.Version) == existing.Model["version"].(float64) {
			return nil
		}
	}

	res, err := r.grafanaClient.NewDashboard(gapi.Dashboard{
		Overwrite: true,
		Model:     model,
		Folder:    status.FolderId,
		Message:   "Updated by Grafana Operator. ResourceVersion: " + dashboard.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("failed to put dashboard: %w", err)
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
	if err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}

	delete(dashboard.Status.Instances, r.instanceKey)
	return r.kubeClient.Status().Update(r.ctx, dashboard)
}

func (r *grafanaClientImpl) getDashboardContent(dashboard *v1beta1.GrafanaDashboard) (map[string]interface{}, error) {
	if dashboard.Spec.Json != "" {
		var res map[string]interface{}
		err := json.Unmarshal([]byte(dashboard.Spec.Json), &res)
		return res, err
	} else if dashboard.Spec.GzipJson != nil {
		gzipReader, err := gzip.NewReader(bytes.NewReader(dashboard.Spec.GzipJson))
		if err != nil {
			return nil, fmt.Errorf("failed to read gzip content: %w", err)
		}
		var res map[string]interface{}
		err = json.NewDecoder(gzipReader).Decode(&res)
		return res, err
	} else if dashboard.Spec.URL != "" {
		return getRemoteDashboard(r.ctx, dashboard.Spec.URL)
	} else if dashboard.Spec.GrafanaCom != nil {
		return getGrafanaComDashboard(r.ctx, dashboard.Spec.GrafanaCom)
	}

	return nil, fmt.Errorf("unable to find source for dashboard content")
}

func getGrafanaComDashboard(ctx context.Context, source *v1beta1.GrafanaComDashboardSpec) (map[string]interface{}, error) {
	if source.Revision == nil {
		rev, err := getLatestGrafanaComRevision(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest revision for dashboard id %d: %w", source.Id, err)
		}
		source.Revision = &rev
	}

	url := fmt.Sprintf("%s/%d/revisions/%d/download", grafanaComDashboardApiUrlRoot, source.Id, source.Revision)
	return getRemoteDashboard(ctx, url)
}

// This is an incomplete representation of the expected response,
// including only fields we care about.
type listDashboardRevisionsResponse struct {
	Items []dashboardRevisionItem `json:"items"`
}

type dashboardRevisionItem struct {
	Revision int `json:"revision"`
}

func getLatestGrafanaComRevision(ctx context.Context, source *v1beta1.GrafanaComDashboardSpec) (int, error) {
	url := fmt.Sprintf("%s/%d/revisions", grafanaComDashboardApiUrlRoot, source.Id)
	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to make request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("request to list available grafana dashboard revisions failed with status code '%d'", resp.StatusCode)
	}

	var listResponse listDashboardRevisionsResponse
	err = json.NewDecoder(resp.Body).Decode(&listResponse)
	if err != nil {
		return -1, err
	}

	var max int
	for _, i := range listResponse.Items {
		if i.Revision > max {
			max = i.Revision
		}
	}

	return max, nil
}

func getRemoteDashboard(ctx context.Context, url string) (map[string]interface{}, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("request to '%s' failed with status code '%d'", url, resp.StatusCode)
	}

	var dashboard map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&dashboard)
	return dashboard, err
}
