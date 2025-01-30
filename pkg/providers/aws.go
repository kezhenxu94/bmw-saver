package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	pkgk8s "github.com/kezhenxu94/bmw-saver/pkg/kubernetes"
)

// AWSProvider implements the CloudProvider interface for AWS EKS
type AWSProvider struct {
	awsConfig   aws.Config
	clusterName string
	kubeConfig  *rest.Config
	eksClients  map[string]*eks.Client // region -> client
	clientMu    sync.RWMutex
}

// NodeGroupConfig represents the configuration for an EKS node group
type NodeGroupConfig struct {
	DesiredSize int32                         `json:"desiredSize"`
	Autoscaling *types.NodegroupScalingConfig `json:"autoscaling,omitempty"`
}

// getEKSClient returns an EKS client for the given region, creating it if necessary
func (p *AWSProvider) getEKSClient(region string) (*eks.Client, error) {
	p.clientMu.RLock()
	client, ok := p.eksClients[region]
	p.clientMu.RUnlock()
	if ok {
		return client, nil
	}

	p.clientMu.Lock()
	defer p.clientMu.Unlock()

	// Check again in case another goroutine created it
	if client, ok = p.eksClients[region]; ok {
		return client, nil
	}

	// Create new client for this region
	cfg := p.awsConfig.Copy()
	cfg.Region = region
	client = eks.NewFromConfig(cfg)
	p.eksClients[region] = client

	return client, nil
}

// getNodeRegion gets the region from a node's labels
func (p *AWSProvider) getNodeRegion(ctx context.Context, nodeName string) (string, error) {
	clientset, err := kubernetes.NewForConfig(p.kubeConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get node %s: %v", nodeName, err)
	}

	region := node.Labels["topology.kubernetes.io/region"]
	if region == "" {
		return "", fmt.Errorf("region label not found on node %s", nodeName)
	}

	return region, nil
}

// NewAWSProvider creates a new AWS provider instance
func NewAWSProvider() (*AWSProvider, error) {
	ctx := context.Background()

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %v", err)
	}

	// Get cluster name from environment
	clusterName := os.Getenv("EKS_CLUSTER_NAME")
	if clusterName == "" {
		return nil, fmt.Errorf("EKS_CLUSTER_NAME environment variable is required")
	}

	// Get kubeconfig
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %v", err)
	}

	return &AWSProvider{
		awsConfig:   cfg,
		clusterName: clusterName,
		kubeConfig:  kubeConfig,
		eksClients:  make(map[string]*eks.Client),
	}, nil
}

// ScaleNodePool scales an EKS node group to the specified count
func (p *AWSProvider) ScaleNodePool(ctx context.Context, nodeGroupName string, count int32) error {
	// Get nodes in the node group to find region
	nodes, err := p.getNodesInNodeGroup(ctx, nodeGroupName)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %v", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes found in node group %s", nodeGroupName)
	}

	// Get region from first node
	region, err := p.getNodeRegion(ctx, nodes[0].Name)
	if err != nil {
		return fmt.Errorf("failed to get region: %v", err)
	}

	// Get EKS client for this region
	eksClient, err := p.getEKSClient(region)
	if err != nil {
		return fmt.Errorf("failed to get EKS client: %v", err)
	}

	// Save current configuration before scaling
	if err = p.saveNodeGroupConfig(ctx, nodeGroupName); err != nil {
		return fmt.Errorf("failed to save node group config: %v", err)
	}

	// Check and log current node group status
	nodeGroup, err := eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   &p.clusterName,
		NodegroupName: &nodeGroupName,
	})
	if err != nil {
		return fmt.Errorf("failed to describe node group: %v", err)
	}

	slog.Info("Current node group status",
		"node_group", nodeGroupName,
		"status", nodeGroup.Nodegroup.Status,
		"health", nodeGroup.Nodegroup.Health,
	)

	// Disable autoscaling if enabled
	if nodeGroup.Nodegroup.ScalingConfig != nil && nodeGroup.Nodegroup.ScalingConfig.MinSize != nil {
		_, err = eksClient.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
			ClusterName:   &p.clusterName,
			NodegroupName: &nodeGroupName,
			ScalingConfig: &types.NodegroupScalingConfig{
				MinSize:     &count,
				MaxSize:     &count,
				DesiredSize: &count,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to update node group scaling config: %v", err)
		}
		slog.Info("Disabled autoscaling for node group", "node_group", nodeGroupName)
	}

	// Get nodes in the node group
	nodesInGroup, err := p.getNodesInNodeGroup(ctx, nodeGroupName)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %v", err)
	}

	// Drain excess nodes
	nodesToDrain := len(nodesInGroup) - int(count)
	if nodesToDrain > 0 {
		for i := 0; i < nodesToDrain && i < len(nodesInGroup); i++ {
			if err = pkgk8s.DrainNode(ctx, p.kubeConfig, nodesInGroup[i].Name); err != nil {
				slog.Error("Failed to drain node", "node", nodesInGroup[i].Name, "error", err)
				continue
			}
		}
	}

	// Wait for node group to be active before updating
	waiter := eks.NewNodegroupActiveWaiter(eksClient)
	if err = waiter.Wait(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   &p.clusterName,
		NodegroupName: &nodeGroupName,
	}, 10*time.Minute); err != nil {
		return fmt.Errorf("failed waiting for node group to be active: %v", err)
	}

	// Verify status after waiting
	nodeGroup, err = eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   &p.clusterName,
		NodegroupName: &nodeGroupName,
	})
	if err != nil {
		return fmt.Errorf("failed to describe node group after waiting: %v", err)
	}

	slog.Info("Node group status after waiting",
		"node_group", nodeGroupName,
		"status", nodeGroup.Nodegroup.Status,
		"health", nodeGroup.Nodegroup.Health,
	)

	// Update node group size
	_, err = eksClient.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName:   &p.clusterName,
		NodegroupName: &nodeGroupName,
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: &count,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to scale node group: %v", err)
	}

	slog.Info("Scaled node group", "node_group", nodeGroupName, "count", count)
	return nil
}

// RestoreNodePool restores an EKS node group to its saved configuration
func (p *AWSProvider) RestoreNodePool(ctx context.Context, nodeGroupName string) error {
	// Get saved config from ConfigMap
	clientset, err := kubernetes.NewForConfig(p.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	configMap, err := clientset.CoreV1().ConfigMaps(os.Getenv("NAMESPACE")).Get(ctx,
		fmt.Sprintf("%s%s", ConfigMapNamePrefix, nodeGroupName), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return &ErrNoSavedState{NodePool: nodeGroupName}
		}
		return fmt.Errorf("failed to get saved config: %v", err)
	}

	var savedConfig NodeGroupConfig
	if err = json.Unmarshal([]byte(configMap.Data["config"]), &savedConfig); err != nil {
		return fmt.Errorf("failed to parse saved config: %v", err)
	}

	// Update node group configuration
	input := &eks.UpdateNodegroupConfigInput{
		ClusterName:   &p.clusterName,
		NodegroupName: &nodeGroupName,
		ScalingConfig: &types.NodegroupScalingConfig{
			DesiredSize: &savedConfig.DesiredSize,
		},
	}

	if savedConfig.Autoscaling != nil {
		input.ScalingConfig.MinSize = savedConfig.Autoscaling.MinSize
		input.ScalingConfig.MaxSize = savedConfig.Autoscaling.MaxSize
	}

	// Get nodes in the node group to find region
	nodes, err := p.getNodesInNodeGroup(ctx, nodeGroupName)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %v", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes found in node group %s", nodeGroupName)
	}

	// Get region from first node
	region, err := p.getNodeRegion(ctx, nodes[0].Name)
	if err != nil {
		return fmt.Errorf("failed to get region: %v", err)
	}

	// Get EKS client for this region
	eksClient, err := p.getEKSClient(region)
	if err != nil {
		return fmt.Errorf("failed to get EKS client: %v", err)
	}

	_, err = eksClient.UpdateNodegroupConfig(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to restore node group: %v", err)
	}

	slog.Info("Restored node group configuration",
		"node_group", nodeGroupName,
		"desired_size", savedConfig.DesiredSize,
	)
	return nil
}

func (p *AWSProvider) saveNodeGroupConfig(ctx context.Context, nodeGroupName string) error {
	// Get nodes in the node group to find region
	nodes, err := p.getNodesInNodeGroup(ctx, nodeGroupName)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %v", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes found in node group %s", nodeGroupName)
	}

	// Get region from first node
	region, err := p.getNodeRegion(ctx, nodes[0].Name)
	if err != nil {
		return fmt.Errorf("failed to get region: %v", err)
	}

	// Get EKS client for this region
	eksClient, err := p.getEKSClient(region)
	if err != nil {
		return fmt.Errorf("failed to get EKS client: %v", err)
	}

	nodeGroup, err := eksClient.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   &p.clusterName,
		NodegroupName: &nodeGroupName,
	})
	if err != nil {
		return fmt.Errorf("failed to describe node group: %v", err)
	}

	config := NodeGroupConfig{
		DesiredSize: *nodeGroup.Nodegroup.ScalingConfig.DesiredSize,
	}

	if nodeGroup.Nodegroup.ScalingConfig != nil {
		config.Autoscaling = &types.NodegroupScalingConfig{
			MinSize: nodeGroup.Nodegroup.ScalingConfig.MinSize,
			MaxSize: nodeGroup.Nodegroup.ScalingConfig.MaxSize,
		}
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s", ConfigMapNamePrefix, nodeGroupName),
			Namespace: os.Getenv("NAMESPACE"),
		},
		Data: map[string]string{
			"config": encodeNodeGroupConfig(config),
		},
	}

	if err := pkgk8s.CreateConfigMap(ctx, p.kubeConfig, configMap); err != nil {
		return fmt.Errorf("failed to save node group config: %v", err)
	}

	slog.Info("Saved node group configuration",
		"node_group", nodeGroupName,
		"config_map", configMap.Name,
	)
	return nil
}

func (p *AWSProvider) getNodesInNodeGroup(ctx context.Context, nodeGroupName string) ([]corev1.Node, error) {
	clientset, err := kubernetes.NewForConfig(p.kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	labelSelector := fmt.Sprintf("eks.amazonaws.com/nodegroup=%s", nodeGroupName)
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %v", err)
	}

	return nodes.Items, nil
}

func encodeNodeGroupConfig(config NodeGroupConfig) string {
	data, err := json.Marshal(config)
	if err != nil {
		slog.Error("Failed to marshal node group config", "error", err)
		return ""
	}
	return string(data)
}
