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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type OperatorStageName string

type OperatorStageStatus string

const (
	OperatorStageGrafanaConfig  OperatorStageName = "config"
	OperatorStageAdminUser      OperatorStageName = "admin user"
	OperatorStagePvc            OperatorStageName = "pvc"
	OperatorStageServiceAccount OperatorStageName = "service account"
	OperatorStageService        OperatorStageName = "service"
	OperatorStageIngress        OperatorStageName = "ingress"
	OperatorStagePlugins        OperatorStageName = "plugins"
	OperatorStageDeployment     OperatorStageName = "deployment"
)

const (
	OperatorStageResultSuccess    OperatorStageStatus = "success"
	OperatorStageResultFailed     OperatorStageStatus = "failed"
	OperatorStageResultInProgress OperatorStageStatus = "in progress"
)

// temporary values passed between reconciler stages
type OperatorReconcileVars struct {
	// used to restart the Grafana container when the config changes
	ConfigHash string

	// env var value for installed plugins
	Plugins string
}

// GrafanaSpec defines the desired state of Grafana
type GrafanaSpec struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	Config                map[string]map[string]string `json:"config"`
	Containers            []v1.Container               `json:"containers,omitempty"`
	Ingress               *IngressNetworkingV1         `json:"ingress,omitempty"`
	Route                 *RouteOpenshiftV1            `json:"route,omitempty"`
	Service               *ServiceV1                   `json:"service,omitempty"`
	Deployment            *DeploymentV1                `json:"deployment,omitempty"`
	PersistentVolumeClaim *PersistentVolumeClaimV1     `json:"persistentVolumeClaim,omitempty"`
	ServiceAccount        *ServiceAccountV1            `json:"serviceAccount,omitempty"`
	Client                *GrafanaClient               `json:"client,omitempty"`
	InitResources         *v1.ResourceRequirements     `json:"initResources,omitempty"`
	Secrets               []string                     `json:"secrets,omitempty"`
	ConfigMaps            []string                     `json:"configMaps,omitempty"`
	Jsonnet               *JsonnetConfig               `json:"jsonnet,omitempty"`
	GrafanaContainer      *GrafanaContainer            `json:"grafanaContainer,omitempty"`
	ManagedNamespaces     []string                     `json:"managedNamespaces,omitempty"`
}

type GrafanaContainer struct {
	BaseImage         string                   `json:"baseImage,omitempty"`
	InitImage         string                   `json:"initImage,omitempty"`
	Resources         *v1.ResourceRequirements `json:"resources,omitempty"`
	ReadinessProbe    *v1.Probe                `json:"readinessProbe,omitempty"`
	LivenessProbeSpec *v1.Probe                `json:"livenessProbe,omitempty"`
}

type ReadinessProbeSpec struct {
	InitialDelaySeconds *int32       `json:"initialDelaySeconds,omitempty"`
	TimeOutSeconds      *int32       `json:"timeoutSeconds,omitempty"`
	PeriodSeconds       *int32       `json:"periodSeconds,omitempty"`
	SuccessThreshold    *int32       `json:"successThreshold,omitempty"`
	FailureThreshold    *int32       `json:"failureThreshold,omitempty"`
	Scheme              v1.URIScheme `json:"scheme,omitempty"`
}
type LivenessProbeSpec struct {
	InitialDelaySeconds *int32       `json:"initialDelaySeconds,omitempty"`
	TimeOutSeconds      *int32       `json:"timeoutSeconds,omitempty"`
	PeriodSeconds       *int32       `json:"periodSeconds,omitempty"`
	SuccessThreshold    *int32       `json:"successThreshold,omitempty"`
	FailureThreshold    *int32       `json:"failureThreshold,omitempty"`
	Scheme              v1.URIScheme `json:"scheme,omitempty"`
}

type JsonnetConfig struct {
	LibraryLabelSelector *metav1.LabelSelector `json:"libraryLabelSelector,omitempty"`
}

// GrafanaClient contains the Grafana API client settings
type GrafanaClient struct {
	// +nullable
	TimeoutSeconds *int `json:"timeout,omitempty"`
	// +nullable
	PreferIngress *bool `json:"preferIngress,omitempty"`
}

// GrafanaStatus defines the observed state of Grafana
type GrafanaStatus struct {
	Stage       OperatorStageName   `json:"stage,omitempty"`
	StageStatus OperatorStageStatus `json:"stageStatus,omitempty"`
	LastMessage string              `json:"lastMessage,omitempty"`
	AdminUrl    string              `json:"adminUrl,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Grafana is the Schema for the grafanas API
type Grafana struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GrafanaSpec   `json:"spec,omitempty"`
	Status            GrafanaStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GrafanaList contains a list of Grafana
type GrafanaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Grafana `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Grafana{}, &GrafanaList{})
}

func (r *Grafana) PreferIngress() bool {
	return r.Spec.Client != nil && r.Spec.Client.PreferIngress != nil && *r.Spec.Client.PreferIngress
}
