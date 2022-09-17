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
	"crypto/sha256"
	"fmt"

	gapi "github.com/grafana/grafana-api-golang-client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GrafanaPlayListSpec defines the desired state of GrafanaPlayList
type GrafanaPlayListSpec struct {
	PlayList *GrafanaPlayListInternal `json:"playList,omitempty"`

	// selects Grafanas for import
	InstanceSelector *metav1.LabelSelector `json:"instanceSelector,omitempty"`
}

type GrafanaPlayListInternal struct {
	// Name of the playlist
	Name string `json:"name"`
	// Interval how often the playlist should change dashboard
	Interval string `json:"interval"`
	// DashboardList is a list of dashboards that should be in the playlist
	Items []PlaylistDashboards `json:"dashboardsList,omitempty"`
}

type PlaylistDashboards struct {
	// +kubebuilder:validation:Enum=dashboard_by_id;dashboard_by_tag
	Type  string `json:"type"`
	Value string `json:"value"`
	// The order of the dashboard in the playlist
	Order int `json:"order"`
	// The title of the dashboard in the playlist
	Title string `json:"title"`
}

// TODO is this layer useful?
//type DashboardsList []PlaylistDashboards

// GrafanaPlayListStatus defines the observed state of GrafanaPlayList
type GrafanaPlayListStatus struct {
	Hash string `json:"hash,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// GrafanaPlayList is the Schema for the GrafanaPlayLists API
type GrafanaPlayList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GrafanaPlayListSpec   `json:"spec,omitempty"`
	Status GrafanaPlayListStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GrafanaPlayListList contains a list of GrafanaPlayList
type GrafanaPlayLists struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GrafanaPlayList `json:"items"`
}

func (in *GrafanaPlayList) Hash() string {
	hash := sha256.New()

	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (in *GrafanaPlayList) Unchanged() bool {
	return in.Hash() == in.Status.Hash
}

func (u *GrafanaPlayListInternal) PlayListConverter() gapi.Playlist {
	dashboardItems := playListDashboards(u.Items)

	playList := gapi.Playlist{
		Name:     u.Name,
		Interval: u.Interval,
		Items:    dashboardItems,
	}
	return playList
}

func playListDashboards(playlistDashboards []PlaylistDashboards) []gapi.PlaylistItem {
	var clientPlayList []gapi.PlaylistItem
	for _, d := range playlistDashboards {
		item := gapi.PlaylistItem{
			Type:  d.Type,
			Value: d.Value,
			Order: d.Order,
			Title: d.Title,
		}
		clientPlayList = append(clientPlayList, item)
	}
	return clientPlayList
}

func init() {
	SchemeBuilder.Register(&GrafanaPlayList{}, &GrafanaPlayLists{})
}
