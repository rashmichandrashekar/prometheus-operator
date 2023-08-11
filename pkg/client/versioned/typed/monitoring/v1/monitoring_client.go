// Copyright The prometheus-operator Authors
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

// Code generated by client-gen. DO NOT EDIT.

package v1

import (
	"net/http"

	v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/scheme"
	"k8s.io/apimachinery/pkg/runtime/schema"
	rest "k8s.io/client-go/rest"
)

type MonitoringV1Interface interface {
	RESTClient() rest.Interface
	AlertmanagersGetter
	PodMonitorsGetter
	ProbesGetter
	PrometheusesGetter
	PrometheusRulesGetter
	ServiceMonitorsGetter
	ThanosRulersGetter
}

// MonitoringV1Client is used to interact with features provided by the monitoring.coreos.com group.
type MonitoringV1Client struct {
	restClient rest.Interface
}

func (c *MonitoringV1Client) Alertmanagers(namespace string) AlertmanagerInterface {
	return newAlertmanagers(c, namespace)
}

func (c *MonitoringV1Client) PodMonitors(namespace string) PodMonitorInterface {
	return newPodMonitors(c, namespace)
}

func (c *MonitoringV1Client) Probes(namespace string) ProbeInterface {
	return newProbes(c, namespace)
}

func (c *MonitoringV1Client) Prometheuses(namespace string) PrometheusInterface {
	return newPrometheuses(c, namespace)
}

func (c *MonitoringV1Client) PrometheusRules(namespace string) PrometheusRuleInterface {
	return newPrometheusRules(c, namespace)
}

func (c *MonitoringV1Client) ServiceMonitors(namespace string) ServiceMonitorInterface {
	return newServiceMonitors(c, namespace)
}

func (c *MonitoringV1Client) ThanosRulers(namespace string) ThanosRulerInterface {
	return newThanosRulers(c, namespace)
}

// NewForConfig creates a new MonitoringV1Client for the given config.
// NewForConfig is equivalent to NewForConfigAndClient(c, httpClient),
// where httpClient was generated with rest.HTTPClientFor(c).
func NewForConfig(c *rest.Config) (*MonitoringV1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	httpClient, err := rest.HTTPClientFor(&config)
	if err != nil {
		return nil, err
	}
	return NewForConfigAndClient(&config, httpClient)
}

// NewForConfigAndClient creates a new MonitoringV1Client for the given config and http client.
// Note the http client provided takes precedence over the configured transport values.
func NewForConfigAndClient(c *rest.Config, h *http.Client) (*MonitoringV1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	client, err := rest.RESTClientForConfigAndClient(&config, h)
	if err != nil {
		return nil, err
	}
	return &MonitoringV1Client{client}, nil
}

// NewForConfigOrDie creates a new MonitoringV1Client for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *MonitoringV1Client {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// New creates a new MonitoringV1Client for the given RESTClient.
func New(c rest.Interface) *MonitoringV1Client {
	return &MonitoringV1Client{c}
}

func setConfigDefaults(config *rest.Config) error {
	//gv := v1.SchemeGroupVersion
	gv := schema.GroupVersion{Group: "azmonitoring.coreos.com", Version: Version}
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	return nil
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *MonitoringV1Client) RESTClient() rest.Interface {
	if c == nil {
		return nil
	}
	return c.restClient
}
