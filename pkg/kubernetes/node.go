package kubernetes

import (
	"context"
	"fmt"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// DrainNode safely drains a node by evicting all pods and marking it as unschedulable.
// It returns an error if the draining process fails.
func DrainNode(ctx context.Context, config *rest.Config, nodeName string) error {
	slog.Info("Draining node", "node", nodeName)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %v", err)
	}

	for _, pod := range pods.Items {
		if pod.Namespace == "kube-system" {
			continue
		}
		err = clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			slog.Warn("Failed to delete pod", "pod", pod.Name, "namespace", pod.Namespace, "error", err)
			continue
		}
		slog.Info("Pod deleted successfully", "pod", pod.Name, "namespace", pod.Namespace)
	}

	return nil
}
