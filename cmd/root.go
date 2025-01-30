package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kezhenxu94/bmw-saver/pkg/config"
	"github.com/kezhenxu94/bmw-saver/pkg/controller"
)

var (
	configFile string
	logLevel   string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "bmw-saver",
	Short: "A Kubernetes node auto-scaler based on work hours",
	Long: `BMW-Saver is a tool that automatically scales Kubernetes node pools
based on configured work hours. It supports GKE, AWS, and Azure clusters,
helping you save costs during off-work hours.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Setup logging
		level := slog.LevelInfo
		switch logLevel {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
		slog.SetDefault(logger)
	},
	RunE: run,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "config.yaml", "Path to the configuration file")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "Log level (debug, info, warn, error)")
}

func run(cmd *cobra.Command, args []string) error {
	slog.Debug("Starting application", "config_file", configFile)

	// Create Kubernetes client
	client, err := getKubernetesClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	// Read initial configuration
	cfg, err := config.ReadConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	// Create controller
	controller, err := controller.NewScalingController(client, cfg)
	if err != nil {
		return fmt.Errorf("failed to create controller: %v", err)
	}

	// Set up config watcher
	watcher := config.NewWatcher(configFile, client)
	watcher.OnConfigChange(controller.UpdateConfig)

	// Start the watcher and controller
	ctx := context.Background()
	errGroup, ctx := errgroup.WithContext(ctx)

	errGroup.Go(func() error {
		return watcher.Start(ctx)
	})

	errGroup.Go(func() error {
		return controller.Run()
	})

	return errGroup.Wait()
}

func getKubernetesClient() (*kubernetes.Clientset, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get Kubernetes config (neither local nor in-cluster): %v", err)
		}
	}

	return kubernetes.NewForConfig(config)
}
