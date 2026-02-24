package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	scrapeInterval = flag.Duration("scrape-interval", 30*time.Second, "Scrape interval")
	listenAddr     = flag.String("listen-address", ":9100", "HTTP listen address")
	enableKubelet  = flag.Bool("enable-kubelet", true, "Scrape kubelet metrics via API server proxy")
	enableCadvisor = flag.Bool("enable-cadvisor", true, "Scrape cAdvisor metrics via API server proxy")
	excludePhases  = flag.String("exclude-phases", "Succeeded,Failed", "Comma-separated pod phases to exclude from aggregation")
)

var (
	nodeCPUUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "k8s_node_cpu_usage_cores",
			Help: "Aggregated CPU usage (cores) per node from kubelet/cAdvisor.",
		},
		[]string{"node"},
	)
	nodeMemUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "k8s_node_memory_usage_bytes",
			Help: "Aggregated memory working set (bytes) per node from kubelet/cAdvisor.",
		},
		[]string{"node"},
	)
	nodePodCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "k8s_node_active_pods",
			Help: "Number of non-terminal pods per node.",
		},
		[]string{"node"},
	)
	scrapeErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "k8s_ai_exporter_scrape_errors_total",
			Help: "Total scrape errors by target.",
		},
		[]string{"target"},
	)
)

func init() {
	prometheus.MustRegister(nodeCPUUsage, nodeMemUsage, nodePodCount, scrapeErrors)
}

func main() {
	flag.Parse()

	cfg, err := inClusterOrKubeconfig()
	if err != nil {
		log.Fatalf("cannot create kube config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("cannot create clientset: %v", err)
	}

	go func() {
		ticker := time.NewTicker(*scrapeInterval)
		defer ticker.Stop()
		for {
			if err := scrapeAndAggregate(context.Background(), cfg, clientset); err != nil {
				log.Printf("scrape error: %v", err)
			}
			<-ticker.C
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	log.Printf("Starting exporter on %s (kubelet=%v cadvisor=%v)", *listenAddr, *enableKubelet, *enableCadvisor)
	log.Fatal(http.ListenAndServe(*listenAddr, nil))
}

func inClusterOrKubeconfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = home + "/.kube/config"
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func scrapeAndAggregate(ctx context.Context, cfg *rest.Config, clientset *kubernetes.Clientset) error {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	exclude := make(map[corev1.PodPhase]bool)
	for _, p := range strings.Split(*excludePhases, ",") {
		phase := corev1.PodPhase(strings.TrimSpace(p))
		if phase != "" {
			exclude[phase] = true
		}
	}

	nodeCounts := make(map[string]float64)
	for _, p := range pods.Items {
		if exclude[p.Status.Phase] {
			continue
		}
		if p.Spec.NodeName == "" {
			continue
		}
		nodeCounts[p.Spec.NodeName]++
	}

	nodeCPU := make(map[string]float64)
	nodeMem := make(map[string]float64)

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return err
	}

	baseURL := strings.TrimSuffix(cfg.Host, "/")
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}

	for _, node := range nodes.Items {
		name := node.Name
		nodeCPU[name] = 0
		nodeMem[name] = 0

		if *enableCadvisor {
			url := fmt.Sprintf("%s/api/v1/nodes/%s/proxy/metrics/cadvisor", baseURL, name)
			cpu, mem, err := scrapeCadvisorMetrics(ctx, client, url)
			if err != nil {
				scrapeErrors.WithLabelValues("cadvisor:" + name).Inc()
				log.Printf("cadvisor %s: %v", name, err)
			} else {
				nodeCPU[name] += cpu
				nodeMem[name] += mem
			}
		}
		if *enableKubelet && !*enableCadvisor {
			url := fmt.Sprintf("%s/api/v1/nodes/%s/proxy/metrics", baseURL, name)
			cpu, mem, err := scrapeKubeletMetrics(ctx, client, url)
			if err != nil {
				scrapeErrors.WithLabelValues("kubelet:" + name).Inc()
				log.Printf("kubelet %s: %v", name, err)
			} else {
				nodeCPU[name] += cpu
				nodeMem[name] += mem
			}
		}
	}

	for node, count := range nodeCounts {
		nodePodCount.WithLabelValues(node).Set(count)
	}
	for node, v := range nodeCPU {
		nodeCPUUsage.WithLabelValues(node).Set(v)
	}
	for node, v := range nodeMem {
		nodeMemUsage.WithLabelValues(node).Set(v)
	}

	return nil
}

func scrapeCadvisorMetrics(ctx context.Context, client *http.Client, url string) (cpu, mem float64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return parseContainerMetrics(resp.Body, "container_cpu_usage_seconds_total", "container_memory_working_set_bytes")
}

func scrapeKubeletMetrics(ctx context.Context, client *http.Client, url string) (cpu, mem float64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return parseContainerMetrics(resp.Body, "container_cpu_usage_seconds_total", "container_memory_working_set_bytes")
}

func parseContainerMetrics(body interface {
	Read(p []byte) (n int, err error)
}, cpuMetric, memMetric string) (cpuTotal, memTotal float64, err error) {
	scanner := bufio.NewScanner(body)
	var cpuSum, memSum float64
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, cpuMetric) {
			v := parsePrometheusValue(line)
			cpuSum += v
		}
		if strings.HasPrefix(line, memMetric) {
			v := parsePrometheusValue(line)
			memSum += v
		}
	}
	return cpuSum, memSum, scanner.Err()
}

func parsePrometheusValue(line string) float64 {
	idx := strings.LastIndex(line, " ")
	if idx == -1 {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(line[idx+1:]), 64)
	return v
}
