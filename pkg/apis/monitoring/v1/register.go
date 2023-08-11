// Copyright 2018 The prometheus-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1

import (
	// "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion is the group version used to register these objects
//var SchemeGroupVersion = schema.GroupVersion{Group: monitoring.GroupName, Version: Version}

// var SchemeGroupVersion = schema.GroupVersion{Group: "azmonitoring.coreos.com", Version: Version}

// var SchemeGroupVersion = schema.GroupVersion{}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	myvar := schema.GroupVersion{Group: "azmonitoring.coreos.com", Version: Version}
	return myvar.WithResource(resource).GroupResource()
}

var (
	// localSchemeBuilder and AddToScheme will stay in k8s.io/kubernetes.
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder
	AddToScheme        = localSchemeBuilder.AddToScheme
)

func init() {
	// We only register manually written functions here. The registration of the
	// generated functions takes place in the generated files. The separation
	// makes the code compile even when the generated files are missing.
	localSchemeBuilder.Register(addKnownTypes)
}

// func CustomInit(customGroupName string) {
// 	//SchemeGroupVersion = schema.GroupVersion{Group: customGroupName, Version: Version}
// 	localSchemeBuilder.Register(addKnownTypes)
// }

// func SetCustomGroup(customGroupName string) {
// 	CustomSchemeGroupVersion = schema.GroupVersion{Group: customGroupName, Version: Version}
// 	localSchemeBuilder.Register(addKnownTypesCustom)
// }

// Adds the list of known types to api.Scheme.
// func addKnownTypesCustom(scheme *runtime.Scheme) error {
// 	scheme.AddKnownTypes(CustomSchemeGroupVersion,
// 		&Prometheus{},
// 		&PrometheusList{},
// 		&ServiceMonitor{},
// 		&ServiceMonitorList{},
// 		&PodMonitor{},
// 		&PodMonitorList{},
// 		&Probe{},
// 		&ProbeList{},
// 		&Alertmanager{},
// 		&AlertmanagerList{},
// 		&PrometheusRule{},
// 		&PrometheusRuleList{},
// 		&ThanosRuler{},
// 		&ThanosRulerList{},
// 	)
// 	metav1.AddToGroupVersion(scheme, CustomSchemeGroupVersion)
// 	return nil
// }

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	CustomSchemeGroupVersion := schema.GroupVersion{Group: "azmonitoring.coreos.com", Version: Version}
	// scheme.AddKnownTypes(SchemeGroupVersion,
	scheme.AddKnownTypes(CustomSchemeGroupVersion,
		&Prometheus{},
		&PrometheusList{},
		&ServiceMonitor{},
		&ServiceMonitorList{},
		&PodMonitor{},
		&PodMonitorList{},
		&Probe{},
		&ProbeList{},
		&Alertmanager{},
		&AlertmanagerList{},
		&PrometheusRule{},
		&PrometheusRuleList{},
		&ThanosRuler{},
		&ThanosRulerList{},
	)
	// metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	metav1.AddToGroupVersion(scheme, CustomSchemeGroupVersion)

	return nil
}
