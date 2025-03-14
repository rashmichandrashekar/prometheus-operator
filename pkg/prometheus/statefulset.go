// Copyright 2016 The prometheus-operator Authors
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

package prometheus

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/prometheus-operator/prometheus-operator/pkg/k8sutil"
	"github.com/prometheus-operator/prometheus-operator/pkg/operator"
)

const (
	StorageDir   = "/prometheus"
	ConfDir      = "/etc/prometheus/config"
	ConfOutDir   = "/etc/prometheus/config_out"
	WebConfigDir = "/etc/prometheus/web_config"
	tlsAssetsDir = "/etc/prometheus/certs"
	//TODO: RulesDir should be moved to the server package, since it is not used by the agent.
	// It is here at the moment because promcfg uses it, and moving as is will cause import cycle error.
	RulesDir                 = "/etc/prometheus/rules"
	secretsDir               = "/etc/prometheus/secrets/"
	configmapsDir            = "/etc/prometheus/configmaps/"
	ConfigFilename           = "prometheus.yaml.gz"
	ConfigEnvsubstFilename   = "prometheus.env.yaml"
	SSetInputHashName        = "prometheus-operator-input-hash"
	DefaultPortName          = "web"
	DefaultQueryLogDirectory = "/var/log/prometheus"
)

var (
	ShardLabelName                = "operator.prometheus.io/shard"
	PrometheusNameLabelName       = "operator.prometheus.io/name"
	PrometheusModeLabeLName       = "operator.prometheus.io/mode"
	PrometheusK8sLabelName        = "app.kubernetes.io/name"
	ProbeTimeoutSeconds     int32 = 3
	LabelPrometheusName           = "prometheus-name"
)

func ExpectedStatefulSetShardNames(
	p monitoringv1.PrometheusInterface,
) []string {
	res := []string{}
	for i := int32(0); i < shardsNumber(p); i++ {
		res = append(res, prometheusNameByShard(p, i))
	}

	return res
}

// shardsNumber returns the normalized number of shards.
func shardsNumber(
	p monitoringv1.PrometheusInterface,
) int32 {
	cpf := p.GetCommonPrometheusFields()

	if ptr.Deref(cpf.Shards, 1) <= 1 {
		return 1
	}

	return *cpf.Shards
}

// ReplicasNumberPtr returns a ptr to the normalized number of replicas.
func ReplicasNumberPtr(
	p monitoringv1.PrometheusInterface,
) *int32 {
	cpf := p.GetCommonPrometheusFields()

	replicas := ptr.Deref(cpf.Replicas, 1)
	if replicas < 0 {
		replicas = 1
	}

	return &replicas
}

func prometheusNameByShard(p monitoringv1.PrometheusInterface, shard int32) string {
	base := prefixedName(p)
	if shard == 0 {
		return base
	}
	return fmt.Sprintf("%s-shard-%d", base, shard)
}

func compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := operator.GzipConfig(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to gzip config: %w", err)
	}

	return buf.Bytes(), nil
}

func MakeConfigurationSecret(p monitoringv1.PrometheusInterface, config Config, data []byte) (*v1.Secret, error) {
	promConfig, err := compress(data)
	if err != nil {
		return nil, err
	}

	s := &v1.Secret{
		Data: map[string][]byte{
			ConfigFilename: promConfig,
		},
	}

	operator.UpdateObject(
		s,
		operator.WithLabels(config.Labels),
		operator.WithAnnotations(config.Annotations),
		operator.WithManagingOwner(p),
		operator.WithName(ConfigSecretName(p)),
	)

	return s, nil
}

func ConfigSecretName(p monitoringv1.PrometheusInterface) string {
	return prefixedName(p)
}

func TLSAssetsSecretName(p monitoringv1.PrometheusInterface) string {
	return fmt.Sprintf("%s-tls-assets", prefixedName(p))
}

func WebConfigSecretName(p monitoringv1.PrometheusInterface) string {
	return fmt.Sprintf("%s-web-config", prefixedName(p))
}

func VolumeName(p monitoringv1.PrometheusInterface) string {
	return fmt.Sprintf("%s-db", prefixedName(p))
}

func prefixedName(p monitoringv1.PrometheusInterface) string {
	return fmt.Sprintf("%s-%s", prefix(p), p.GetObjectMeta().GetName())
}

func prefix(p monitoringv1.PrometheusInterface) string {
	switch p.(type) {
	case *monitoringv1.Prometheus:
		return "prometheus"
	case *monitoringv1alpha1.PrometheusAgent:
		return "prom-agent"
	default:
		panic("unknown prometheus type")
	}
}

// TODO: Storage methods should be moved to server package.
// It is stil here because promcfg still uses it.
func SubPathForStorage(s *monitoringv1.StorageSpec) string {
	//nolint:staticcheck // Ignore SA1019 this field is marked as deprecated.
	if s == nil || s.DisableMountSubPath {
		return ""
	}

	return "prometheus-db"
}

// TODO: QueryLogFile methods should be moved to server package.
// They are still here because promcfg is using them.
func UsesDefaultQueryLogVolume(queryLogFile string) bool {
	return queryLogFile != "" && filepath.Dir(queryLogFile) == "."
}

func queryLogFilePath(queryLogFile string) string {
	if !UsesDefaultQueryLogVolume(queryLogFile) {
		return queryLogFile
	}

	return filepath.Join(DefaultQueryLogDirectory, queryLogFile)
}

// BuildCommonPrometheusArgs builds a slice of arguments that are common between Prometheus Server and Agent.
func BuildCommonPrometheusArgs(cpf monitoringv1.CommonPrometheusFields, cg *ConfigGenerator) []monitoringv1.Argument {
	promArgs := []monitoringv1.Argument{
		{Name: "web.console.templates", Value: "/etc/prometheus/consoles"},
		{Name: "web.console.libraries", Value: "/etc/prometheus/console_libraries"},
		{Name: "config.file", Value: path.Join(ConfOutDir, ConfigEnvsubstFilename)},
	}

	if ptr.Deref(cpf.ReloadStrategy, monitoringv1.HTTPReloadStrategyType) == monitoringv1.HTTPReloadStrategyType {
		promArgs = append(promArgs, monitoringv1.Argument{Name: "web.enable-lifecycle"})
	}

	if cpf.Web != nil {
		if cpf.Web.PageTitle != nil {
			promArgs = cg.WithMinimumVersion("2.6.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "web.page-title", Value: *cpf.Web.PageTitle})
		}

		if cpf.Web.MaxConnections != nil {
			promArgs = append(promArgs, monitoringv1.Argument{Name: "web.max-connections", Value: fmt.Sprintf("%d", *cpf.Web.MaxConnections)})
		}
	}

	if cpf.EnableRemoteWriteReceiver {
		promArgs = cg.WithMinimumVersion("2.33.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "web.enable-remote-write-receiver"})
	}

	if len(cpf.EnableFeatures) > 0 {
		promArgs = cg.WithMinimumVersion("2.25.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "enable-feature", Value: strings.Join(cpf.EnableFeatures[:], ",")})
	}

	if cpf.ExternalURL != "" {
		promArgs = append(promArgs, monitoringv1.Argument{Name: "web.external-url", Value: cpf.ExternalURL})
	}

	promArgs = append(promArgs, monitoringv1.Argument{Name: "web.route-prefix", Value: cpf.WebRoutePrefix()})

	if cpf.LogLevel != "" && cpf.LogLevel != "info" {
		promArgs = append(promArgs, monitoringv1.Argument{Name: "log.level", Value: cpf.LogLevel})
	}

	if cpf.LogFormat != "" && cpf.LogFormat != "logfmt" {
		promArgs = cg.WithMinimumVersion("2.6.0").AppendCommandlineArgument(promArgs, monitoringv1.Argument{Name: "log.format", Value: cpf.LogFormat})
	}

	if cpf.ListenLocal {
		promArgs = append(promArgs, monitoringv1.Argument{Name: "web.listen-address", Value: "127.0.0.1:9090"})
	}

	return promArgs
}

// BuildCommonVolumes returns a set of volumes to be mounted on statefulset spec that are common between Prometheus Server and Agent
func BuildCommonVolumes(p monitoringv1.PrometheusInterface, tlsAssetSecrets []string) ([]v1.Volume, []v1.VolumeMount, error) {
	cpf := p.GetCommonPrometheusFields()

	assetsVolume := v1.Volume{
		Name: "tls-assets",
		VolumeSource: v1.VolumeSource{
			Projected: &v1.ProjectedVolumeSource{
				Sources: []v1.VolumeProjection{},
			},
		},
	}
	for _, assetShard := range tlsAssetSecrets {
		assetsVolume.Projected.Sources = append(assetsVolume.Projected.Sources,
			v1.VolumeProjection{
				Secret: &v1.SecretProjection{
					LocalObjectReference: v1.LocalObjectReference{Name: assetShard},
				},
			})
	}

	volumes := []v1.Volume{
		{
			Name: "config",
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: ConfigSecretName(p),
				},
			},
		},
		assetsVolume,
		{
			Name: "config-out",
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{
					// tmpfs is used here to avoid writing sensitive data into disk.
					Medium: v1.StorageMediumMemory,
				},
			},
		},
	}

	volName := VolumeClaimName(p, cpf)

	promVolumeMounts := []v1.VolumeMount{
		{
			Name:      "config-out",
			ReadOnly:  true,
			MountPath: ConfOutDir,
		},
		{
			Name:      "tls-assets",
			ReadOnly:  true,
			MountPath: tlsAssetsDir,
		},
		{
			Name:      volName,
			MountPath: StorageDir,
			SubPath:   SubPathForStorage(cpf.Storage),
		},
	}

	promVolumeMounts = append(promVolumeMounts, cpf.VolumeMounts...)

	// Mount related secrets
	rn := k8sutil.NewResourceNamerWithPrefix("secret")
	for _, s := range cpf.Secrets {
		name, err := rn.DNS1123Label(s)
		if err != nil {
			return nil, nil, err
		}

		volumes = append(volumes, v1.Volume{
			Name: name,
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: s,
				},
			},
		})
		promVolumeMounts = append(promVolumeMounts, v1.VolumeMount{
			Name:      name,
			ReadOnly:  true,
			MountPath: secretsDir + s,
		})
	}

	rn = k8sutil.NewResourceNamerWithPrefix("configmap")
	for _, c := range cpf.ConfigMaps {
		name, err := rn.DNS1123Label(c)
		if err != nil {
			return nil, nil, err
		}

		volumes = append(volumes, v1.Volume{
			Name: name,
			VolumeSource: v1.VolumeSource{
				ConfigMap: &v1.ConfigMapVolumeSource{
					LocalObjectReference: v1.LocalObjectReference{
						Name: c,
					},
				},
			},
		})
		promVolumeMounts = append(promVolumeMounts, v1.VolumeMount{
			Name:      name,
			ReadOnly:  true,
			MountPath: configmapsDir + c,
		})
	}

	return volumes, promVolumeMounts, nil
}

func VolumeClaimName(p monitoringv1.PrometheusInterface, cpf monitoringv1.CommonPrometheusFields) string {
	volName := VolumeName(p)
	if cpf.Storage != nil {
		if cpf.Storage.VolumeClaimTemplate.Name != "" {
			volName = cpf.Storage.VolumeClaimTemplate.Name
		}
	}
	return volName
}

func ProbeHandler(probePath string, cpf monitoringv1.CommonPrometheusFields, webConfigGenerator *ConfigGenerator) v1.ProbeHandler {
	probePath = path.Clean(cpf.WebRoutePrefix() + probePath)
	handler := v1.ProbeHandler{}
	if cpf.ListenLocal {
		probeURL := url.URL{
			Scheme: "http",
			Host:   "localhost:9090",
			Path:   probePath,
		}
		handler.Exec = &v1.ExecAction{
			Command: []string{
				"sh",
				"-c",
				fmt.Sprintf(
					`if [ -x "$(command -v curl)" ]; then exec %s; elif [ -x "$(command -v wget)" ]; then exec %s; else exit 1; fi`,
					operator.CurlProber(probeURL.String()),
					operator.WgetProber(probeURL.String()),
				),
			},
		}
		return handler
	}

	handler.HTTPGet = &v1.HTTPGetAction{
		Path: probePath,
		Port: intstr.FromString(cpf.PortName),
	}
	if cpf.Web != nil && cpf.Web.TLSConfig != nil && webConfigGenerator.IsCompatible() {
		handler.HTTPGet.Scheme = v1.URISchemeHTTPS
	}
	return handler
}

func BuildPodMetadata(cpf monitoringv1.CommonPrometheusFields, cg *ConfigGenerator) (map[string]string, map[string]string) {
	podAnnotations := map[string]string{
		"kubectl.kubernetes.io/default-container": "prometheus",
	}
	podLabels := map[string]string{
		"app.kubernetes.io/version": cg.version.String(),
	}

	if cpf.PodMetadata != nil {
		for k, v := range cpf.PodMetadata.Labels {
			podLabels[k] = v
		}
		for k, v := range cpf.PodMetadata.Annotations {
			podAnnotations[k] = v
		}
	}

	return podAnnotations, podLabels
}

func BuildConfigReloader(
	p monitoringv1.PrometheusInterface,
	c *Config,
	initContainer bool,
	mounts []v1.VolumeMount,
	watchedDirectories []string,
	opts ...operator.ReloaderOption,
) v1.Container {
	cpf := p.GetCommonPrometheusFields()

	reloaderOptions := []operator.ReloaderOption{
		operator.ReloaderConfig(c.ReloaderConfig),
		operator.LogFormat(cpf.LogFormat),
		operator.LogLevel(cpf.LogLevel),
		operator.VolumeMounts(mounts),
		operator.ConfigFile(path.Join(ConfDir, ConfigFilename)),
		operator.ConfigEnvsubstFile(path.Join(ConfOutDir, ConfigEnvsubstFilename)),
		operator.WatchedDirectories(watchedDirectories),
		operator.ImagePullPolicy(cpf.ImagePullPolicy),
	}
	reloaderOptions = append(reloaderOptions, opts...)

	name := "config-reloader"
	if initContainer {
		name = "init-config-reloader"
		reloaderOptions = append(reloaderOptions, operator.ReloaderRunOnce())
		return operator.CreateConfigReloader(name, reloaderOptions...)
	}

	if ptr.Deref(cpf.ReloadStrategy, monitoringv1.HTTPReloadStrategyType) == monitoringv1.ProcessSignalReloadStrategyType {
		reloaderOptions = append(reloaderOptions,
			operator.ReloaderUseSignal(),
		)
		reloaderOptions = append(reloaderOptions,
			operator.RuntimeInfoURL(url.URL{
				Scheme: cpf.PrometheusURIScheme(),
				Host:   c.LocalHost + ":9090",
				Path:   path.Clean(cpf.WebRoutePrefix() + "/api/v1/status/runtimeinfo"),
			}),
		)
	} else {
		reloaderOptions = append(reloaderOptions,
			operator.ListenLocal(cpf.ListenLocal),
			operator.LocalHost(c.LocalHost),
		)
		reloaderOptions = append(reloaderOptions,
			operator.ReloaderURL(url.URL{
				Scheme: cpf.PrometheusURIScheme(),
				Host:   c.LocalHost + ":9090",
				Path:   path.Clean(cpf.WebRoutePrefix() + "/-/reload"),
			}),
		)
	}

	return operator.CreateConfigReloader(name, reloaderOptions...)
}

func ShareProcessNamespace(p monitoringv1.PrometheusInterface) *bool {
	return ptr.To(
		ptr.Deref(
			p.GetCommonPrometheusFields().ReloadStrategy,
			monitoringv1.HTTPReloadStrategyType,
		) == monitoringv1.ProcessSignalReloadStrategyType,
	)
}

func MakeK8sTopologySpreadConstraint(selectorLabels map[string]string, tscs []monitoringv1.TopologySpreadConstraint) []v1.TopologySpreadConstraint {

	coreTscs := make([]v1.TopologySpreadConstraint, 0, len(tscs))

	for _, tsc := range tscs {
		if tsc.AdditionalLabelSelectors == nil {
			coreTscs = append(coreTscs, v1.TopologySpreadConstraint(tsc.CoreV1TopologySpreadConstraint))
			continue
		}

		if tsc.LabelSelector == nil {
			tsc.LabelSelector = &metav1.LabelSelector{
				MatchLabels: make(map[string]string),
			}
		}

		for key, value := range selectorLabels {
			if *tsc.AdditionalLabelSelectors == monitoringv1.ResourceNameLabelSelector && key == ShardLabelName {
				continue
			}
			tsc.LabelSelector.MatchLabels[key] = value
		}

		coreTscs = append(coreTscs, v1.TopologySpreadConstraint(tsc.CoreV1TopologySpreadConstraint))
	}

	return coreTscs
}

func GetStatupProbePeriodSecondsAndFailureThreshold(cfp monitoringv1.CommonPrometheusFields) (int32, int32) {
	var startupPeriodSeconds float64 = 15
	var startupFailureThreshold float64 = 60

	maximumStartupDurationSeconds := float64(ptr.Deref(cfp.MaximumStartupDurationSeconds, 0))

	if maximumStartupDurationSeconds >= 60 {
		startupFailureThreshold = math.Ceil(maximumStartupDurationSeconds / 60)
		startupPeriodSeconds = math.Ceil(maximumStartupDurationSeconds / startupFailureThreshold)
	}

	return int32(startupPeriodSeconds), int32(startupFailureThreshold)
}
