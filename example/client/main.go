package main

import (
	"context"
	"fmt"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus-operator/prometheus-operator/pkg/assets"
	"github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/prometheus-operator/prometheus-operator/pkg/informers"
	"github.com/prometheus-operator/prometheus-operator/pkg/listwatch"
	"github.com/prometheus-operator/prometheus-operator/pkg/operator"
	"github.com/prometheus-operator/prometheus-operator/pkg/prometheus"
	prometheusgoclient "github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//"k8s.io/apimachinery/pkg/labels"
	"github.com/prometheus/common/model"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"os"
	"time"
)

const DefaultResyncTime = 5 * time.Minute
const resyncPeriod = 5 * time.Minute
const DefaultCRScrapeInterval model.Duration = model.Duration(time.Second * 30)

func getInformers(factory informers.FactoriesForNamespaces) (map[string]*informers.ForResource, error) {
	serviceMonitorInformers, err := informers.NewInformersForResource(factory, monitoringv1.SchemeGroupVersion.WithResource(monitoringv1.ServiceMonitorName))
	if err != nil {
		return nil, err
	}

	podMonitorInformers, err := informers.NewInformersForResource(factory, monitoringv1.SchemeGroupVersion.WithResource(monitoringv1.PodMonitorName))
	if err != nil {
		return nil, err
	}

	return map[string]*informers.ForResource{
		monitoringv1.ServiceMonitorName: serviceMonitorInformers,
		monitoringv1.PodMonitorName:     podMonitorInformers,
	}, nil
}

// func getSelector(s map[string]string) labels.Selector {
// 	if s == nil {
// 		return labels.NewSelector()
// 	}
// 	return labels.SelectorFromSet(s)
// }

func getNamespaceInformer(ctx context.Context, allowList map[string]struct{}, promOperatorLogger log.Logger, clientset kubernetes.Interface, operatorMetrics *operator.Metrics) (cache.SharedIndexInformer, error) {
	kubernetesVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	lw, _, err := listwatch.NewNamespaceListWatchFromClient(
		ctx,
		promOperatorLogger,
		*kubernetesVersion,
		clientset.CoreV1(),
		clientset.AuthorizationV1().SelfSubjectAccessReviews(),
		allowList,
		map[string]struct{}{},
	)
	if err != nil {
		return nil, err
	}

	return cache.NewSharedIndexInformer(
		operatorMetrics.NewInstrumentedListerWatcher(lw),
		&corev1.Namespace{}, resyncPeriod, cache.Indexers{},
	), nil

}

// Run the program with the default group name:
// go run ./example/client/.
//
// Run the program with a custom group name:
// go run -ldflags="-s -X github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring.GroupName=monitoring.example.com" ./example/client/.
func main() {

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	cs, err := versioned.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	smons, err := cs.MonitoringV1().ServiceMonitors("").List(context.Background(), v1.ListOptions{})
	if err != nil {
		panic(err)
	}
	for _, smon := range smons.Items {
		fmt.Printf("%s: %s/%s\n", smon.GetObjectKind().GroupVersionKind(), smon.GetNamespace(), smon.GetName())
	}

	factory := informers.NewMonitoringInformerFactories(map[string]struct{}{v1.NamespaceAll: {}}, map[string]struct{}{}, cs, DefaultResyncTime, nil) //TODO decide what strategy to use regarding namespaces

	monitoringInformers, err := getInformers(factory)
	if err != nil {
		panic(err)
	}

	prom := &monitoringv1.Prometheus{
		Spec: monitoringv1.PrometheusSpec{
			CommonPrometheusFields: monitoringv1.CommonPrometheusFields{
				ScrapeInterval: monitoringv1.Duration(DefaultCRScrapeInterval),
				ServiceMonitorSelector: &v1.LabelSelector{
					MatchLabels: nil,
				},
				PodMonitorSelector: &v1.LabelSelector{
					MatchLabels: nil,
				},
				ServiceMonitorNamespaceSelector: &v1.LabelSelector{
					MatchLabels: nil,
				},
				PodMonitorNamespaceSelector: &v1.LabelSelector{
					MatchLabels: nil,
				},
			},
		},
	}

	promOperatorLogger := level.NewFilter(log.NewLogfmtLogger(os.Stderr), level.AllowWarn())

	if err != nil {
		panic(err)
	}
	var resourceSelector *prometheus.ResourceSelector

	store := assets.NewStore(clientset.CoreV1(), clientset.CoreV1())
	promRegisterer := prometheusgoclient.NewRegistry()
	operatorMetrics := operator.NewMetrics(promRegisterer)
	ctx := context.Background()
	nsMonInf, err := getNamespaceInformer(ctx, map[string]struct{}{v1.NamespaceAll: {}}, promOperatorLogger, clientset, operatorMetrics)
	if err != nil {
		panic(err)

	} else {
		resourceSelector = prometheus.NewResourceSelector(promOperatorLogger, prom, store, nsMonInf, operatorMetrics)
	}

	//servMonSelector := getSelector(nil)

	// smRetrieveErr := monitoringInformers[monitoringv1.ServiceMonitorName].ListAll(servMonSelector, func(sm interface{}) {
	// 	monitor := sm.(*monitoringv1.ServiceMonitor)
	// 	fmt.Printf("%s: %s/%s\n", monitor.Name, monitor.Namespace, "")
	// })
	// if smRetrieveErr != nil {
	// 	panic(err)
	// }

	serviceMonitorInstances, err := resourceSelector.SelectServiceMonitors(ctx, monitoringInformers[monitoringv1.ServiceMonitorName].ListAllByNamespace)
	if err != nil {
		panic(err)
	}
	for key, monitor := range serviceMonitorInstances {
		fmt.Printf("%s: %s/%s\n", key, monitor.Name, monitor.Namespace)
	}

}
