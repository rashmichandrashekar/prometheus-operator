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

package alertmanager

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus-operator/prometheus-operator/pkg/k8sutil"
	"github.com/prometheus-operator/prometheus-operator/pkg/operator"
	"github.com/prometheus-operator/prometheus-operator/pkg/webconfig"
)

const (
	governingServiceName = "alertmanager-operated"

	defaultRetention = "120h"
	defaultPortName  = "web"

	tlsAssetsVolumeName                = "tls-assets"
	tlsAssetsDir                       = "/etc/alertmanager/certs"
	secretsDir                         = "/etc/alertmanager/secrets"
	configmapsDir                      = "/etc/alertmanager/configmaps"
	alertmanagerTemplatesVolumeName    = "notification-templates"
	alertmanagerTemplatesDir           = "/etc/alertmanager/templates"
	webConfigDir                       = "/etc/alertmanager/web_config"
	alertmanagerConfigVolumeName       = "config-volume"
	alertmanagerConfigDir              = "/etc/alertmanager/config"
	alertmanagerConfigOutVolumeName    = "config-out"
	alertmanagerConfigOutDir           = "/etc/alertmanager/config_out"
	alertmanagerConfigFile             = "alertmanager.yaml"
	alertmanagerConfigFileCompressed   = "alertmanager.yaml.gz"
	alertmanagerConfigEnvsubstFilename = "alertmanager.env.yaml"

	alertmanagerStorageDir = "/alertmanager"

	sSetInputHashName = "prometheus-operator-input-hash"
)

var (
	minReplicas         int32 = 1
	probeTimeoutSeconds int32 = 3
)

func makeStatefulSet(logger log.Logger, am *monitoringv1.Alertmanager, config Config, inputHash string, tlsAssetSecrets []string) (*appsv1.StatefulSet, error) {
	// TODO(fabxc): is this the right point to inject defaults?
	// Ideally we would do it before storing but that's currently not possible.
	// Potentially an update handler on first insertion.

	if am.Spec.PortName == "" {
		am.Spec.PortName = defaultPortName
	}
	if am.Spec.Replicas == nil {
		am.Spec.Replicas = &minReplicas
	}
	intZero := int32(0)
	if am.Spec.Replicas != nil && *am.Spec.Replicas < 0 {
		am.Spec.Replicas = &intZero
	}
	// TODO(slashpai): Remove this assignment after v0.60 since this is handled at CRD level
	if am.Spec.Retention == "" {
		am.Spec.Retention = defaultRetention
	}
	if am.Spec.Resources.Requests == nil {
		am.Spec.Resources.Requests = v1.ResourceList{}
	}
	if _, ok := am.Spec.Resources.Requests[v1.ResourceMemory]; !ok {
		am.Spec.Resources.Requests[v1.ResourceMemory] = resource.MustParse("200Mi")
	}

	spec, err := makeStatefulSetSpec(logger, am, config, tlsAssetSecrets)
	if err != nil {
		return nil, err
	}

	annotations := map[string]string{
		sSetInputHashName: inputHash,
	}

	// do not transfer kubectl annotations to the statefulset so it is not
	// pruned by kubectl
	for key, value := range am.ObjectMeta.Annotations {
		if key != sSetInputHashName && !strings.HasPrefix(key, "kubectl.kubernetes.io/") {
			annotations[key] = value
		}
	}

	statefulset := &appsv1.StatefulSet{
		Spec: *spec,
	}
	operator.UpdateObject(
		statefulset,
		operator.WithName(prefixedName(am.Name)),
		operator.WithAnnotations(annotations),
		operator.WithAnnotations(config.Annotations),
		operator.WithLabels(am.Labels),
		operator.WithLabels(config.Labels),
		operator.WithManagingOwner(am),
	)

	if am.Spec.ImagePullSecrets != nil && len(am.Spec.ImagePullSecrets) > 0 {
		statefulset.Spec.Template.Spec.ImagePullSecrets = am.Spec.ImagePullSecrets
	}

	storageSpec := am.Spec.Storage
	if storageSpec == nil {
		statefulset.Spec.Template.Spec.Volumes = append(statefulset.Spec.Template.Spec.Volumes, v1.Volume{
			Name: volumeName(am.Name),
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		})
	} else if storageSpec.EmptyDir != nil {
		emptyDir := storageSpec.EmptyDir
		statefulset.Spec.Template.Spec.Volumes = append(statefulset.Spec.Template.Spec.Volumes, v1.Volume{
			Name: volumeName(am.Name),
			VolumeSource: v1.VolumeSource{
				EmptyDir: emptyDir,
			},
		})
	} else if storageSpec.Ephemeral != nil {
		ephemeral := storageSpec.Ephemeral
		statefulset.Spec.Template.Spec.Volumes = append(statefulset.Spec.Template.Spec.Volumes, v1.Volume{
			Name: volumeName(am.Name),
			VolumeSource: v1.VolumeSource{
				Ephemeral: ephemeral,
			},
		})
	} else {
		pvcTemplate := operator.MakeVolumeClaimTemplate(storageSpec.VolumeClaimTemplate)
		if pvcTemplate.Name == "" {
			pvcTemplate.Name = volumeName(am.Name)
		}
		if storageSpec.VolumeClaimTemplate.Spec.AccessModes == nil {
			pvcTemplate.Spec.AccessModes = []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce}
		} else {
			pvcTemplate.Spec.AccessModes = storageSpec.VolumeClaimTemplate.Spec.AccessModes
		}
		pvcTemplate.Spec.Resources = storageSpec.VolumeClaimTemplate.Spec.Resources
		pvcTemplate.Spec.Selector = storageSpec.VolumeClaimTemplate.Spec.Selector
		statefulset.Spec.VolumeClaimTemplates = append(statefulset.Spec.VolumeClaimTemplates, *pvcTemplate)
	}

	statefulset.Spec.Template.Spec.Volumes = append(statefulset.Spec.Template.Spec.Volumes, am.Spec.Volumes...)

	return statefulset, nil
}

func makeStatefulSetService(a *monitoringv1.Alertmanager, config Config) *v1.Service {
	if a.Spec.PortName == "" {
		a.Spec.PortName = defaultPortName
	}

	svc := &v1.Service{
		Spec: v1.ServiceSpec{
			ClusterIP: "None",
			Ports: []v1.ServicePort{
				{
					Name:       a.Spec.PortName,
					Port:       9093,
					TargetPort: intstr.FromString(a.Spec.PortName),
					Protocol:   v1.ProtocolTCP,
				},
				{
					Name:       "tcp-mesh",
					Port:       9094,
					TargetPort: intstr.FromInt(9094),
					Protocol:   v1.ProtocolTCP,
				},
				{
					Name:       "udp-mesh",
					Port:       9094,
					TargetPort: intstr.FromInt(9094),
					Protocol:   v1.ProtocolUDP,
				},
			},
			Selector: map[string]string{
				"app.kubernetes.io/name": "alertmanager",
			},
		},
	}

	operator.UpdateObject(
		svc,
		operator.WithName(governingServiceName),
		operator.WithAnnotations(config.Annotations),
		operator.WithLabels(map[string]string{"operated-alertmanager": "true"}),
		operator.WithLabels(config.Labels),
		operator.WithOwner(a),
	)

	return svc
}

func makeStatefulSetSpec(logger log.Logger, a *monitoringv1.Alertmanager, config Config, tlsAssetSecrets []string) (*appsv1.StatefulSetSpec, error) {
	amVersion := operator.StringValOrDefault(a.Spec.Version, operator.DefaultAlertmanagerVersion)
	amImagePath, err := operator.BuildImagePath(
		operator.StringPtrValOrDefault(a.Spec.Image, ""),
		operator.StringValOrDefault(a.Spec.BaseImage, config.AlertmanagerDefaultBaseImage),
		amVersion,
		operator.StringValOrDefault(a.Spec.Tag, ""),
		operator.StringValOrDefault(a.Spec.SHA, ""),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build image path: %w", err)
	}

	version, err := semver.ParseTolerant(amVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse alertmanager version: %w", err)
	}

	amArgs := []string{
		fmt.Sprintf("--config.file=%s", path.Join(alertmanagerConfigOutDir, alertmanagerConfigEnvsubstFilename)),
		fmt.Sprintf("--storage.path=%s", alertmanagerStorageDir),
		fmt.Sprintf("--data.retention=%s", a.Spec.Retention),
	}

	if *a.Spec.Replicas == 1 && !a.Spec.ForceEnableClusterMode {
		amArgs = append(amArgs, "--cluster.listen-address=")
	} else {
		amArgs = append(amArgs, "--cluster.listen-address=[$(POD_IP)]:9094")
	}

	if a.Spec.ListenLocal {
		amArgs = append(amArgs, "--web.listen-address=127.0.0.1:9093")
	} else {
		amArgs = append(amArgs, "--web.listen-address=:9093")
	}

	if a.Spec.ExternalURL != "" {
		amArgs = append(amArgs, "--web.external-url="+a.Spec.ExternalURL)
	}

	webRoutePrefix := "/"
	if a.Spec.RoutePrefix != "" {
		webRoutePrefix = a.Spec.RoutePrefix
	}
	amArgs = append(amArgs, fmt.Sprintf("--web.route-prefix=%v", webRoutePrefix))

	web := a.Spec.Web
	if version.GTE(semver.MustParse("0.17.0")) && web != nil && web.GetConcurrency != nil {
		amArgs = append(amArgs, fmt.Sprintf("--web.get-concurrency=%d", *web.GetConcurrency))
	}

	if version.GTE(semver.MustParse("0.17.0")) && web != nil && web.Timeout != nil {
		amArgs = append(amArgs, fmt.Sprintf("--web.timeout=%d", *web.Timeout))
	}

	if a.Spec.LogLevel != "" && a.Spec.LogLevel != "info" {
		amArgs = append(amArgs, fmt.Sprintf("--log.level=%s", a.Spec.LogLevel))
	}

	if version.GTE(semver.MustParse("0.16.0")) {
		if a.Spec.LogFormat != "" && a.Spec.LogFormat != "logfmt" {
			amArgs = append(amArgs, fmt.Sprintf("--log.format=%s", a.Spec.LogFormat))
		}
	}

	if a.Spec.ClusterAdvertiseAddress != "" {
		amArgs = append(amArgs, fmt.Sprintf("--cluster.advertise-address=%s", a.Spec.ClusterAdvertiseAddress))
	}

	if a.Spec.ClusterGossipInterval != "" {
		amArgs = append(amArgs, fmt.Sprintf("--cluster.gossip-interval=%s", a.Spec.ClusterGossipInterval))
	}

	if a.Spec.ClusterPushpullInterval != "" {
		amArgs = append(amArgs, fmt.Sprintf("--cluster.pushpull-interval=%s", a.Spec.ClusterPushpullInterval))
	}

	if a.Spec.ClusterPeerTimeout != "" {
		amArgs = append(amArgs, fmt.Sprintf("--cluster.peer-timeout=%s", a.Spec.ClusterPeerTimeout))
	}

	// If multiple Alertmanager clusters are deployed on the same cluster, it can happen
	// that because pod IP addresses are recycled, an Alertmanager instance from cluster B
	// connects with cluster A.
	// --cluster.label flag was introduced in alertmanager v0.26, this helps to block
	// any traffic that is not meant for the cluster.
	if version.GTE(semver.MustParse("0.26.0")) {
		clusterLabel := fmt.Sprintf("%s/%s", a.Namespace, a.Name)
		if a.Spec.ClusterLabel != nil {
			clusterLabel = *a.Spec.ClusterLabel
		}
		amArgs = append(amArgs, fmt.Sprintf("--cluster.label=%s", clusterLabel))
	}

	isHTTPS := a.Spec.Web != nil && a.Spec.Web.TLSConfig != nil && version.GTE(semver.MustParse("0.22.0"))

	livenessProbeHandler := v1.ProbeHandler{
		HTTPGet: &v1.HTTPGetAction{
			Path: path.Clean(webRoutePrefix + "/-/healthy"),
			Port: intstr.FromString(a.Spec.PortName),
		},
	}

	readinessProbeHandler := v1.ProbeHandler{
		HTTPGet: &v1.HTTPGetAction{
			Path: path.Clean(webRoutePrefix + "/-/ready"),
			Port: intstr.FromString(a.Spec.PortName),
		},
	}

	var livenessProbe *v1.Probe
	var readinessProbe *v1.Probe
	if !a.Spec.ListenLocal {
		livenessProbe = &v1.Probe{
			ProbeHandler:     livenessProbeHandler,
			TimeoutSeconds:   probeTimeoutSeconds,
			FailureThreshold: 10,
		}

		readinessProbe = &v1.Probe{
			ProbeHandler:        readinessProbeHandler,
			InitialDelaySeconds: 3,
			TimeoutSeconds:      3,
			PeriodSeconds:       5,
			FailureThreshold:    10,
		}

		if isHTTPS {
			livenessProbe.HTTPGet.Scheme = v1.URISchemeHTTPS
			readinessProbe.HTTPGet.Scheme = v1.URISchemeHTTPS
		}
	}

	podAnnotations := map[string]string{}
	podLabels := map[string]string{
		"app.kubernetes.io/version": version.String(),
	}
	// In cases where an existing selector label is modified, or a new one is added, new sts cannot match existing pods.
	// We should try to avoid removing such immutable fields whenever possible since doing
	// so forces us to enter the 'recreate cycle' and can potentially lead to downtime.
	// The requirement to make a change here should be carefully evaluated.
	podSelectorLabels := map[string]string{
		"app.kubernetes.io/name":       "alertmanager",
		"app.kubernetes.io/managed-by": "prometheus-operator",
		"app.kubernetes.io/instance":   a.Name,
		"alertmanager":                 a.Name,
	}
	if a.Spec.PodMetadata != nil {
		for k, v := range a.Spec.PodMetadata.Labels {
			podLabels[k] = v
		}
		for k, v := range a.Spec.PodMetadata.Annotations {
			podAnnotations[k] = v
		}
	}
	for k, v := range podSelectorLabels {
		podLabels[k] = v
	}

	podAnnotations["kubectl.kubernetes.io/default-container"] = "alertmanager"

	var operatorInitContainers []v1.Container

	var clusterPeerDomain string
	if config.ClusterDomain != "" {
		clusterPeerDomain = fmt.Sprintf("%s.%s.svc.%s.", governingServiceName, a.Namespace, config.ClusterDomain)
	} else {
		// The default DNS search path is .svc.<cluster domain>
		clusterPeerDomain = governingServiceName
	}
	for i := int32(0); i < *a.Spec.Replicas; i++ {
		amArgs = append(amArgs, fmt.Sprintf("--cluster.peer=%s-%d.%s:9094", prefixedName(a.Name), i, clusterPeerDomain))
	}

	for _, peer := range a.Spec.AdditionalPeers {
		amArgs = append(amArgs, fmt.Sprintf("--cluster.peer=%s", peer))
	}

	ports := []v1.ContainerPort{
		{
			Name:          "mesh-tcp",
			ContainerPort: 9094,
			Protocol:      v1.ProtocolTCP,
		},
		{
			Name:          "mesh-udp",
			ContainerPort: 9094,
			Protocol:      v1.ProtocolUDP,
		},
	}
	if !a.Spec.ListenLocal {
		ports = append([]v1.ContainerPort{
			{
				Name:          a.Spec.PortName,
				ContainerPort: 9093,
				Protocol:      v1.ProtocolTCP,
			},
		}, ports...)
	}

	// Adjust Alertmanager command line args to specified AM version
	//
	// Alertmanager versions < v0.15.0 are only supported on a best effort basis
	// starting with Prometheus Operator v0.30.0.
	switch version.Major {
	case 0:
		if version.Minor < 15 {
			for i := range amArgs {
				// below Alertmanager v0.15.0 peer address port specification is not necessary
				if strings.Contains(amArgs[i], "--cluster.peer") {
					amArgs[i] = strings.TrimSuffix(amArgs[i], ":9094")
				}

				// below Alertmanager v0.15.0 high availability flags are prefixed with 'mesh' instead of 'cluster'
				amArgs[i] = strings.Replace(amArgs[i], "--cluster.", "--mesh.", 1)
			}
		} else {
			// reconnect-timeout was added in 0.15 (https://github.com/prometheus/alertmanager/pull/1384)
			// Override default 6h value to allow AlertManager cluster to
			// quickly remove a cluster member after its pod restarted or during a
			// regular rolling update.
			amArgs = append(amArgs, "--cluster.reconnect-timeout=5m")
		}
		if version.Minor < 13 {
			for i := range amArgs {
				// below Alertmanager v0.13.0 all flags are with single dash.
				amArgs[i] = strings.Replace(amArgs[i], "--", "-", 1)
			}
		}
		if version.Minor < 7 {
			// below Alertmanager v0.7.0 the flag 'web.route-prefix' does not exist
			amArgs = filter(amArgs, func(s string) bool {
				return !strings.Contains(s, "web.route-prefix")
			})
		}
	default:
		return nil, fmt.Errorf("unsupported Alertmanager major version %s", version)
	}

	assetsVolume := v1.Volume{
		Name: tlsAssetsVolumeName,
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
			Name: alertmanagerConfigVolumeName,
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: generatedConfigSecretName(a.Name),
				},
			},
		},
		assetsVolume,
		{
			Name: alertmanagerConfigOutVolumeName,
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{
					// tmpfs is used here to avoid writing sensitive data into disk.
					Medium: v1.StorageMediumMemory,
				},
			},
		},
	}

	volName := volumeName(a.Name)
	if a.Spec.Storage != nil {
		if a.Spec.Storage.VolumeClaimTemplate.Name != "" {
			volName = a.Spec.Storage.VolumeClaimTemplate.Name
		}
	}

	amVolumeMounts := []v1.VolumeMount{
		{
			Name:      alertmanagerConfigVolumeName,
			MountPath: alertmanagerConfigDir,
		},
		{
			Name:      alertmanagerConfigOutVolumeName,
			ReadOnly:  true,
			MountPath: alertmanagerConfigOutDir,
		},
		{
			Name:      tlsAssetsVolumeName,
			ReadOnly:  true,
			MountPath: tlsAssetsDir,
		},
		{
			Name:      volName,
			MountPath: alertmanagerStorageDir,
			SubPath:   subPathForStorage(a.Spec.Storage),
		},
	}

	amCfg := a.Spec.AlertmanagerConfiguration
	if amCfg != nil && len(amCfg.Templates) > 0 {
		sources := []v1.VolumeProjection{}
		keys := sets.Set[string]{}
		for _, v := range amCfg.Templates {
			if v.ConfigMap != nil {
				if keys.Has(v.ConfigMap.Key) {
					level.Debug(logger).Log("msg", fmt.Sprintf("skipping %q due to duplicate key %q", v.ConfigMap.Key, v.ConfigMap.Name))
					continue
				}
				sources = append(sources, v1.VolumeProjection{
					ConfigMap: &v1.ConfigMapProjection{
						LocalObjectReference: v1.LocalObjectReference{
							Name: v.ConfigMap.Name,
						},
						Items: []v1.KeyToPath{{
							Key:  v.ConfigMap.Key,
							Path: v.ConfigMap.Key,
						}},
					},
				})
				keys.Insert(v.ConfigMap.Key)
			}
			if v.Secret != nil {
				if keys.Has(v.Secret.Key) {
					level.Debug(logger).Log("msg", fmt.Sprintf("skipping %q due to duplicate key %q", v.Secret.Key, v.Secret.Name))
					continue
				}
				sources = append(sources, v1.VolumeProjection{
					Secret: &v1.SecretProjection{
						LocalObjectReference: v1.LocalObjectReference{
							Name: v.Secret.Name,
						},
						Items: []v1.KeyToPath{{
							Key:  v.Secret.Key,
							Path: v.Secret.Key,
						}},
					},
				})
				keys.Insert(v.Secret.Key)
			}
		}
		volumes = append(volumes, v1.Volume{
			Name: "notification-templates",
			VolumeSource: v1.VolumeSource{
				Projected: &v1.ProjectedVolumeSource{
					Sources: sources,
				},
			},
		})
		amVolumeMounts = append(amVolumeMounts, v1.VolumeMount{
			Name:      alertmanagerTemplatesVolumeName,
			ReadOnly:  true,
			MountPath: alertmanagerTemplatesDir,
		})
	}

	watchedDirectories := []string{alertmanagerConfigDir}
	configReloaderVolumeMounts := []v1.VolumeMount{
		{
			Name:      alertmanagerConfigVolumeName,
			MountPath: alertmanagerConfigDir,
			ReadOnly:  true,
		},
		{
			Name:      alertmanagerConfigOutVolumeName,
			MountPath: alertmanagerConfigOutDir,
		},
	}

	rn := k8sutil.NewResourceNamerWithPrefix("secret")
	for _, s := range a.Spec.Secrets {
		name, err := rn.DNS1123Label(s)
		if err != nil {
			return nil, err
		}

		volumes = append(volumes, v1.Volume{
			Name: name,
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: s,
				},
			},
		})
		mountPath := path.Join(secretsDir, s)
		mount := v1.VolumeMount{
			Name:      name,
			ReadOnly:  true,
			MountPath: mountPath,
		}
		amVolumeMounts = append(amVolumeMounts, mount)
		configReloaderVolumeMounts = append(configReloaderVolumeMounts, mount)
		watchedDirectories = append(watchedDirectories, mountPath)
	}

	rn = k8sutil.NewResourceNamerWithPrefix("configmap")
	for _, c := range a.Spec.ConfigMaps {
		name, err := rn.DNS1123Label(c)
		if err != nil {
			return nil, err
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
		mountPath := path.Join(configmapsDir, c)
		mount := v1.VolumeMount{
			Name:      name,
			ReadOnly:  true,
			MountPath: mountPath,
		}
		amVolumeMounts = append(amVolumeMounts, mount)
		configReloaderVolumeMounts = append(configReloaderVolumeMounts, mount)
		watchedDirectories = append(watchedDirectories, mountPath)
	}

	amVolumeMounts = append(amVolumeMounts, a.Spec.VolumeMounts...)

	// Mount web config and web TLS credentials as volumes.
	// We always mount the web config file for versions greater than 0.22.0.
	// With this we avoid redeploying alertmanager when reconfiguring between
	// HTTP and HTTPS and vice-versa.
	if version.GTE(semver.MustParse("0.22.0")) {
		var fields monitoringv1.WebConfigFileFields
		if a.Spec.Web != nil {
			fields = a.Spec.Web.WebConfigFileFields
		}

		webConfig, err := webconfig.New(webConfigDir, webConfigSecretName(a.Name), fields)
		if err != nil {
			return nil, err
		}

		confArg, configVol, configMount, err := webConfig.GetMountParameters()
		if err != nil {
			return nil, err
		}
		amArgs = append(amArgs, fmt.Sprintf("--%s=%s", confArg.Name, confArg.Value))
		volumes = append(volumes, configVol...)
		amVolumeMounts = append(amVolumeMounts, configMount...)
	}

	finalSelectorLabels := config.Labels.Merge(podSelectorLabels)
	finalLabels := config.Labels.Merge(podLabels)

	alertmanagerURIScheme := "http"
	if isHTTPS {
		alertmanagerURIScheme = "https"
	}

	defaultContainers := []v1.Container{
		{
			Args:            amArgs,
			Name:            "alertmanager",
			Image:           amImagePath,
			ImagePullPolicy: a.Spec.ImagePullPolicy,
			Ports:           ports,
			VolumeMounts:    amVolumeMounts,
			LivenessProbe:   livenessProbe,
			ReadinessProbe:  readinessProbe,
			Resources:       a.Spec.Resources,
			SecurityContext: &v1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				ReadOnlyRootFilesystem:   ptr.To(true),
				Capabilities: &v1.Capabilities{
					Drop: []v1.Capability{"ALL"},
				},
			},
			Env: []v1.EnvVar{
				{
					// Necessary for '--cluster.listen-address' flag
					Name: "POD_IP",
					ValueFrom: &v1.EnvVarSource{
						FieldRef: &v1.ObjectFieldSelector{
							FieldPath: "status.podIP",
						},
					},
				},
			},
			TerminationMessagePolicy: v1.TerminationMessageFallbackToLogsOnError,
		},
		operator.CreateConfigReloader(
			"config-reloader",
			operator.ReloaderConfig(config.ReloaderConfig),
			operator.ReloaderURL(url.URL{
				Scheme: alertmanagerURIScheme,
				Host:   config.LocalHost + ":9093",
				Path:   path.Clean(webRoutePrefix + "/-/reload"),
			}),
			operator.ListenLocal(a.Spec.ListenLocal),
			operator.LocalHost(config.LocalHost),
			operator.LogFormat(a.Spec.LogFormat),
			operator.LogLevel(a.Spec.LogLevel),
			operator.WatchedDirectories(watchedDirectories),
			operator.VolumeMounts(configReloaderVolumeMounts),
			operator.Shard(-1),
			operator.ConfigFile(path.Join(alertmanagerConfigDir, alertmanagerConfigFileCompressed)),
			operator.ConfigEnvsubstFile(path.Join(alertmanagerConfigOutDir, alertmanagerConfigEnvsubstFilename)),
			operator.ImagePullPolicy(a.Spec.ImagePullPolicy),
		),
	}

	containers, err := k8sutil.MergePatchContainers(defaultContainers, a.Spec.Containers)
	if err != nil {
		return nil, fmt.Errorf("failed to merge containers spec: %w", err)
	}

	var minReadySeconds int32
	if a.Spec.MinReadySeconds != nil {
		minReadySeconds = int32(*a.Spec.MinReadySeconds)
	}

	operatorInitContainers = append(operatorInitContainers,
		operator.CreateConfigReloader(
			"init-config-reloader",
			operator.ReloaderConfig(config.ReloaderConfig),
			operator.ReloaderRunOnce(),
			operator.LogFormat(a.Spec.LogFormat),
			operator.LogLevel(a.Spec.LogLevel),
			operator.WatchedDirectories(watchedDirectories),
			operator.VolumeMounts(configReloaderVolumeMounts),
			operator.Shard(-1),
			operator.ConfigFile(path.Join(alertmanagerConfigDir, alertmanagerConfigFileCompressed)),
			operator.ConfigEnvsubstFile(path.Join(alertmanagerConfigOutDir, alertmanagerConfigEnvsubstFilename)),
			operator.ImagePullPolicy(a.Spec.ImagePullPolicy),
		),
	)

	initContainers, err := k8sutil.MergePatchContainers(operatorInitContainers, a.Spec.InitContainers)
	if err != nil {
		return nil, fmt.Errorf("failed to merge init containers spec: %w", err)
	}

	// PodManagementPolicy is set to Parallel to mitigate issues in kubernetes: https://github.com/kubernetes/kubernetes/issues/60164
	// This is also mentioned as one of limitations of StatefulSets: https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#limitations
	return &appsv1.StatefulSetSpec{
		ServiceName:         governingServiceName,
		Replicas:            a.Spec.Replicas,
		MinReadySeconds:     minReadySeconds,
		PodManagementPolicy: appsv1.ParallelPodManagement,
		UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
			Type: appsv1.RollingUpdateStatefulSetStrategyType,
		},
		Selector: &metav1.LabelSelector{
			MatchLabels: finalSelectorLabels,
		},
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      finalLabels,
				Annotations: podAnnotations,
			},
			Spec: v1.PodSpec{
				AutomountServiceAccountToken:  a.Spec.AutomountServiceAccountToken,
				NodeSelector:                  a.Spec.NodeSelector,
				PriorityClassName:             a.Spec.PriorityClassName,
				TerminationGracePeriodSeconds: ptr.To(int64(120)),
				InitContainers:                initContainers,
				Containers:                    containers,
				Volumes:                       volumes,
				ServiceAccountName:            a.Spec.ServiceAccountName,
				SecurityContext:               a.Spec.SecurityContext,
				Tolerations:                   a.Spec.Tolerations,
				Affinity:                      a.Spec.Affinity,
				TopologySpreadConstraints:     a.Spec.TopologySpreadConstraints,
				HostAliases:                   operator.MakeHostAliases(a.Spec.HostAliases),
			},
		},
	}, nil
}

func defaultConfigSecretName(am *monitoringv1.Alertmanager) string {
	if am.Spec.ConfigSecret == "" {
		return prefixedName(am.Name)
	}

	return am.Spec.ConfigSecret
}

func generatedConfigSecretName(name string) string {
	return prefixedName(name) + "-generated"
}

func webConfigSecretName(name string) string {
	return fmt.Sprintf("%s-web-config", prefixedName(name))
}

func volumeName(name string) string {
	return fmt.Sprintf("%s-db", prefixedName(name))
}

func prefixedName(name string) string {
	return fmt.Sprintf("alertmanager-%s", name)
}

func subPathForStorage(s *monitoringv1.StorageSpec) string {
	if s == nil {
		return ""
	}

	//nolint:staticcheck // Ignore SA1019 this field is marked as deprecated.
	if s.DisableMountSubPath {
		return ""
	}

	return "alertmanager-db"
}

func filter(strings []string, f func(string) bool) []string {
	filteredStrings := make([]string, 0)
	for _, s := range strings {
		if f(s) {
			filteredStrings = append(filteredStrings, s)
		}
	}
	return filteredStrings
}
