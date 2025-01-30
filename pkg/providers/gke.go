package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	container "google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	pkgk8s "github.com/kezhenxu94/bmw-saver/pkg/kubernetes"
)

const (
	// ConfigMapNamePrefix is the prefix for the ConfigMap name
	ConfigMapNamePrefix = "bmw-saver-nodepool-"
	// ConfigMapNamespace is the namespace for the ConfigMap
	ConfigMapNamespace = "bmw-saver"
)

// GKEProvider implements the CloudProvider interface for Google Kubernetes Engine.
type GKEProvider struct {
	service    *container.Service
	projectID  string
	cluster    string
	location   string
	kubeConfig *rest.Config
}

// NodePoolConfig represents the configuration for a node pool
type NodePoolConfig struct {
	NodeCount   int64                          `json:"nodeCount"`
	Autoscaling *container.NodePoolAutoscaling `json:"autoscaling,omitempty"`
}

// NewGKEProvider creates a new GKE provider instance.
// It initializes the GCP client and retrieves cluster information.
func NewGKEProvider() (*GKEProvider, error) {
	ctx := context.Background()
	service, err := container.NewService(ctx, option.WithScopes(container.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create GKE service: %v", err)
	}

	projectID, err := getProjectID()
	if err != nil {
		return nil, fmt.Errorf("failed to get project ID: %v", err)
	}

	cluster, err := getClusterName()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster name: %v", err)
	}

	location, err := getClusterLocation()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster location: %v", err)
	}

	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %v", err)
	}

	slog.Info("GKE provider initialized",
		"project_id", projectID,
		"cluster", cluster,
		"location", location,
	)

	return &GKEProvider{
		service:    service,
		projectID:  projectID,
		cluster:    cluster,
		location:   location,
		kubeConfig: kubeConfig,
	}, nil
}

// getMetadataValue retrieves a value from the GCE metadata server
func getMetadataValue(path string) (string, error) {
	client := &http.Client{
		Timeout: time.Second * 5,
	}
	req, err := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/"+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if e := resp.Body.Close(); err != nil {
			slog.Error("Failed to close response body", "error", e)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server returned status code %d", resp.StatusCode)
	}

	value, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(value), nil
}

func getProjectID() (string, error) {
	return getMetadataValue("project/project-id")
}

func getClusterName() (string, error) {
	return getMetadataValue("instance/attributes/cluster-name")
}

func getClusterLocation() (string, error) {
	return getMetadataValue("instance/attributes/cluster-location")
}

func isClusterBusy(err error) bool {
	if err == nil {
		return false
	}
	if gerr, ok := err.(*googleapi.Error); ok {
		if gerr.Code != 400 {
			return false
		}
		// Check if any of the error items contain CLUSTER_ALREADY_HAS_OPERATION
		for _, item := range gerr.Details {
			if details, ok := item.(map[string]interface{}); ok {
				if reason, ok := details["reason"].(string); ok {
					if reason == "CLUSTER_ALREADY_HAS_OPERATION" {
						return true
					}
				}
			}
		}
	}
	return false
}

// ScaleNodePool scales a GKE node pool to the specified count.
// It handles autoscaling settings and node draining.
func (p *GKEProvider) ScaleNodePool(ctx context.Context, nodePoolName string, count int32) error {
	nodePools, err := p.listNodePools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list node pools: %v", err)
	}

	for _, nodePool := range nodePools {
		slog.Debug("Node pool", "name", nodePool.Name, "size", nodePool.InitialNodeCount)
		if nodePool.Name == nodePoolName {
			slog.Debug("Node pool found", "node_pool", nodePoolName)

			nodes, err := p.getNodesInNodePool(ctx, nodePoolName)
			if err != nil {
				return fmt.Errorf("failed to get nodes in node pool: %v", err)
			}
			slog.Debug("Nodes in node pool", "nodes", nodes)

			if len(nodes) == int(count) {
				slog.Debug("Node pool already at desired size", "node_pool", nodePoolName, "size", count)
				return nil
			}

			for _, node := range nodes {
				slog.Debug("Node", "name", node.Name, "status", node.Status)
				if isNodeCordoned(&node) {
					if err := pkgk8s.DrainNode(ctx, p.kubeConfig, node.Name); err != nil {
						return fmt.Errorf("failed to drain node %s: %v", node.Name, err)
					}
				}
			}

			if err := p.saveNodePoolConfig(ctx, nodePoolName); err != nil {
				return fmt.Errorf("failed to save node pool config: %v", err)
			}

			if nodePool.Autoscaling != nil && nodePool.Autoscaling.Enabled {
				slog.Info("Disabling autoscaling before scaling node pool", "node_pool", nodePoolName)
				if err := p.disableAutoscaling(ctx, nodePoolName); err != nil {
					return fmt.Errorf("failed to disable autoscaling: %v", err)
				}
			}

			if err := p.updateNodePool(ctx, nodePoolName, count); err != nil {
				return fmt.Errorf("failed to update node pool: %v", err)
			}
			return nil
		}
	}

	slog.Warn("Node pool not found", "node_pool", nodePoolName)
	return nil
}

func isNodeCordoned(node *corev1.Node) bool {
	return node.Spec.Unschedulable
}

func (p *GKEProvider) listNodePools(ctx context.Context) ([]*container.NodePool, error) {
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.projectID, p.location, p.cluster)

	resp, err := p.service.Projects.Locations.Clusters.NodePools.List(name).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list node pools: %v", err)
	}

	return resp.NodePools, nil
}

func (p *GKEProvider) updateNodePool(ctx context.Context, nodePoolName string, count int32) error {
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", p.projectID, p.location, p.cluster, nodePoolName)

	request := &container.SetNodePoolSizeRequest{
		NodeCount: int64(count),
	}

	_, err := p.service.Projects.Locations.Clusters.NodePools.SetSize(name, request).Context(ctx).Do()
	if err != nil {
		if isClusterBusy(err) {
			slog.Info("Cluster is busy, will retry in next reconciliation", "node_pool", nodePoolName)
			return nil
		}
		return fmt.Errorf("failed to update node pool: %v", err)
	}

	return nil
}

func (p *GKEProvider) disableAutoscaling(ctx context.Context, nodePoolName string) error {
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", p.projectID, p.location, p.cluster, nodePoolName)

	request := &container.SetNodePoolAutoscalingRequest{
		Autoscaling: &container.NodePoolAutoscaling{
			Enabled: false,
		},
	}

	_, err := p.service.Projects.Locations.Clusters.NodePools.SetAutoscaling(name, request).Context(ctx).Do()
	if err != nil {
		if isClusterBusy(err) {
			slog.Info("Cluster is busy, will retry in next reconciliation", "node_pool", nodePoolName)
			return nil
		}
		return fmt.Errorf("failed to disable autoscaling for node pool: %v", err)
	}

	slog.Info("Disabled autoscaling for node pool", "node_pool", nodePoolName)
	return nil
}

func (p *GKEProvider) saveNodePoolConfig(ctx context.Context, nodePoolName string) error {
	nodePools, err := p.listNodePools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list node pools: %v", err)
	}

	for _, nodePool := range nodePools {
		if nodePool.Name == nodePoolName {
			config := NodePoolConfig{
				NodeCount:   nodePool.InitialNodeCount,
				Autoscaling: nodePool.Autoscaling,
			}

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s%s", ConfigMapNamePrefix, nodePoolName),
					Namespace: os.Getenv("NAMESPACE"),
				},
				Data: map[string]string{
					"config": encodeNodePoolConfig(config),
				},
			}

			if err := pkgk8s.CreateConfigMap(ctx, p.kubeConfig, configMap); err != nil {
				return fmt.Errorf("failed to save node pool config: %v", err)
			}

			slog.Info("Saved node pool configuration to ConfigMap",
				"node_pool", nodePoolName,
				"config_map", configMap.Name,
			)
			return nil
		}
	}

	return fmt.Errorf("node pool %s not found", nodePoolName)
}

func encodeNodePoolConfig(config NodePoolConfig) string {
	data, err := json.Marshal(config)
	if err != nil {
		slog.Error("Failed to marshal node pool config", "error", err)
		return ""
	}
	return string(data)
}

func (p *GKEProvider) getNodesInNodePool(ctx context.Context, nodePoolName string) ([]corev1.Node, error) {
	clientset, err := kubernetes.NewForConfig(p.kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	labelSelector := fmt.Sprintf("cloud.google.com/gke-nodepool=%s", nodePoolName)
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %v", err)
	}

	return nodes.Items, nil
}

// RestoreNodePool restores a GKE node pool to its saved configuration.
// It retrieves the configuration from a ConfigMap and applies it.
func (p *GKEProvider) RestoreNodePool(ctx context.Context, nodePoolName string) error {
	// Get saved config from ConfigMap
	clientset, err := kubernetes.NewForConfig(p.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	configMap, err := clientset.CoreV1().ConfigMaps(os.Getenv("NAMESPACE")).Get(ctx,
		fmt.Sprintf("%s%s", ConfigMapNamePrefix, nodePoolName), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return &ErrNoSavedState{NodePool: nodePoolName}
		}
		return fmt.Errorf("failed to get saved config: %v", err)
	}

	configData := configMap.Data["config"]
	var savedConfig NodePoolConfig
	if err = json.Unmarshal([]byte(configData), &savedConfig); err != nil {
		return fmt.Errorf("failed to parse saved config: %v", err)
	}

	// Check current node pool state
	nodePools, err := p.listNodePools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list node pools: %v", err)
	}

	var currentPool *container.NodePool
	for _, pool := range nodePools {
		if pool.Name == nodePoolName {
			currentPool = pool
			break
		}
	}

	if currentPool == nil {
		return fmt.Errorf("node pool %s not found", nodePoolName)
	}

	// Check if node pool is already at desired state
	isAutoscalingMatch := (currentPool.Autoscaling == nil && savedConfig.Autoscaling == nil) ||
		(currentPool.Autoscaling != nil && savedConfig.Autoscaling != nil &&
			currentPool.Autoscaling.Enabled == savedConfig.Autoscaling.Enabled)
	isNodeCountMatch := savedConfig.Autoscaling != nil && savedConfig.Autoscaling.Enabled ||
		currentPool.InitialNodeCount == savedConfig.NodeCount

	if isAutoscalingMatch && isNodeCountMatch {
		slog.Debug("Node pool already at desired state",
			"node_pool", nodePoolName,
			"node_count", savedConfig.NodeCount,
			"autoscaling_enabled", savedConfig.Autoscaling != nil && savedConfig.Autoscaling.Enabled,
		)
		return nil
	}

	// Restore node count and autoscaling settings
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s",
		p.projectID, p.location, p.cluster, nodePoolName)

	if savedConfig.Autoscaling != nil && savedConfig.Autoscaling.Enabled {
		// Only update autoscaling if it's different from current state
		if !isAutoscalingMatch {
			request := &container.SetNodePoolAutoscalingRequest{
				Autoscaling: savedConfig.Autoscaling,
			}
			_, err = p.service.Projects.Locations.Clusters.NodePools.SetAutoscaling(name, request).Context(ctx).Do()
			if err != nil {
				if isClusterBusy(err) {
					slog.Info("Cluster is busy, will retry in next reconciliation", "node_pool", nodePoolName)
					return nil
				}
				return fmt.Errorf("failed to restore autoscaling: %v", err)
			}
			slog.Info("Restored autoscaling settings", "node_pool", nodePoolName)
		}
	} else {
		// Only set node count when autoscaling is disabled
		request := &container.SetNodePoolSizeRequest{
			NodeCount: savedConfig.NodeCount,
		}
		_, err = p.service.Projects.Locations.Clusters.NodePools.SetSize(name, request).Context(ctx).Do()
		if err != nil {
			if isClusterBusy(err) {
				slog.Info("Cluster is busy, will retry in next reconciliation", "node_pool", nodePoolName)
				return nil
			}
			return fmt.Errorf("failed to restore node count: %v", err)
		}
		slog.Info("Restored node count", "node_pool", nodePoolName, "count", savedConfig.NodeCount)
	}

	return nil
}
