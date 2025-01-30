package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Watcher manages configuration changes from both files and Kubernetes ConfigMaps.
type Watcher struct {
	configPath string
	namespace  string
	client     kubernetes.Interface
	callbacks  []func(Config)
	mu         sync.RWMutex
}

// NewWatcher creates a new configuration watcher for the specified config path and Kubernetes client.
func NewWatcher(configPath string, client kubernetes.Interface) *Watcher {
	return &Watcher{
		configPath: configPath,
		namespace:  os.Getenv("NAMESPACE"),
		client:     client,
		callbacks:  make([]func(Config), 0),
	}
}

// OnConfigChange registers a callback function that will be called whenever the configuration changes.
func (w *Watcher) OnConfigChange(callback func(Config)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks = append(w.callbacks, callback)
}

// notifyCallbacks calls all registered callbacks with the provided configuration.
// It holds a read lock to ensure thread safety when accessing the callbacks slice.
func (w *Watcher) notifyCallbacks(cfg Config) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, callback := range w.callbacks {
		callback(cfg)
	}
}

// Start begins watching for configuration changes from both file and ConfigMap sources.
// It blocks until the context is cancelled or an error occurs.
func (w *Watcher) Start(ctx context.Context) error {
	// Start both file and configmap watchers
	errCh := make(chan error, 2)

	go func() {
		errCh <- w.watchFile(ctx)
	}()

	go func() {
		errCh <- w.watchConfigMap(ctx)
	}()

	// Wait for either context cancellation or an error
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (w *Watcher) watchFile(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %v", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			slog.Error("Failed to close file watcher", "error", err)
		}
	}()

	// Watch the directory containing the config file
	configDir := filepath.Dir(w.configPath)
	if err := watcher.Add(configDir); err != nil {
		return fmt.Errorf("failed to watch config directory: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-watcher.Events:
			if event.Name == w.configPath && (event.Op&fsnotify.Write == fsnotify.Write) {
				slog.Info("Config file changed, reloading", "path", w.configPath)
				if cfg, err := ReadConfig(w.configPath); err == nil {
					w.notifyCallbacks(cfg)
				} else {
					slog.Error("Failed to reload config file", "error", err)
				}
			}
		case err := <-watcher.Errors:
			slog.Error("File watcher error", "error", err)
		}
	}
}

func (w *Watcher) watchConfigMap(ctx context.Context) error {
	factory := informers.NewSharedInformerFactoryWithOptions(
		w.client,
		0,
		informers.WithNamespace(w.namespace),
	)

	informer := factory.Core().V1().ConfigMaps().Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			newCM := new.(*corev1.ConfigMap)
			if newCM.Name == "bmw-saver-config" {
				slog.Info("ConfigMap updated, reloading config")
				if cfg, err := ReadConfigFromBytes([]byte(newCM.Data["config.yaml"])); err == nil {
					w.notifyCallbacks(cfg)
				} else {
					slog.Error("Failed to parse updated config", "error", err)
				}
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %v", err)
	}

	informer.Run(ctx.Done())
	return nil
}
