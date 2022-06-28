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

package v1beta1

import (
	"context"
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GrafanaDashboardSpec defines the desired state of GrafanaDashboard
type GrafanaDashboardSpec struct {
	// dashboard json
	Json string `json:"json,omitempty"`

	// dashboard remote url
	URL string `json:"url,omitempty"`

	// grafana.com dashboard id
	GrafanaCom *GrafanaComDashboardSpec `json:"grafanaCom,omitempty"`

	// dashboard folder
	Folder *GrafanaDashboardFolderSpec `json:"folder,omitempty"`

	// selects Grafanas for import
	InstanceSelector *metav1.LabelSelector `json:"instanceSelector,omitempty"`

	// plugins
	Plugins PluginList `json:"plugins,omitempty"`
}

type GrafanaComDashboardSpec struct{}

type GrafanaDashboardFolderSpec struct {
	Name string
	UID  string
}

// GrafanaDashboardStatus defines the observed state of GrafanaDashboard
type GrafanaDashboardStatus struct {
	GrafanaVersion int64
	GrafanaUID     string
	FolderId       int64
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// GrafanaDashboard is the Schema for the grafanadashboards API
type GrafanaDashboard struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GrafanaDashboardSpec   `json:"spec,omitempty"`
	Status GrafanaDashboardStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GrafanaDashboardList contains a list of GrafanaDashboard
type GrafanaDashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GrafanaDashboard `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GrafanaDashboard{}, &GrafanaDashboardList{})
}

func (r *GrafanaDashboard) GetContent(ctx context.Context) (map[string]interface{}, error) {
	if r.Spec.Json != "" {
		var res map[string]interface{}
		err := json.Unmarshal([]byte(r.Spec.Json), &res)
		return res, err
	} else if r.Spec.URL != "" {
		return getRemoteDashboard(ctx, r.Spec.URL)
	} else if r.Spec.GrafanaCom != nil {
		return getGrafanaComDashboard(ctx, r.Spec.GrafanaCom)
	}

	return nil, nil
}

func getGrafanaComDashboard(ctx context.Context, spec *string) (map[string]interface{}, error) {
	url := "https://grafana.com/" + *spec
	return getRemoteDashboard(ctx, url)
}

func getRemoteDashboard(ctx context.Context, url string) (map[string]interface{}, error) {
	return nil, nil
}
