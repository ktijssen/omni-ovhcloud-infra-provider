// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package main is the entry point for the OVHcloud Omni infra provider.
package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/siderolabs/omni/client/pkg/client"
	"github.com/siderolabs/omni/client/pkg/infra"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/config"
	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider"
	"github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider/meta"
	osfacade "github.com/ktijssen/omni-ovhcloud-infra-provider/internal/pkg/provider/openstack"
)

//go:embed data/schema.json
var schema string

//go:embed data/icon.svg
var icon []byte

var rootCmd = &cobra.Command{
	Use:          "provider",
	Short:        "OVHcloud Omni infrastructure provider",
	Long:         `Connects to Omni as an infra provider and manages instances on OVHcloud Public Cloud (OpenStack).`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		logger, err := zap.NewProductionConfig().Build(zap.AddStacktrace(zapcore.ErrorLevel))
		if err != nil {
			return fmt.Errorf("failed to create logger: %w", err)
		}

		if cfg.omniAPIEndpoint == "" {
			return fmt.Errorf("--omni-api-endpoint flag is not set (or OMNI_ENDPOINT env var)")
		}

		providerCfg, err := config.Load(cfg.configFile)
		if err != nil {
			return err
		}

		if err = providerCfg.Validate(); err != nil {
			return fmt.Errorf("invalid OVHcloud configuration: %w", err)
		}

		osClient := osfacade.New(providerCfg.OpenStack)

		provisioner := provider.NewProvisioner(osClient)

		ip, err := infra.NewProvider(meta.ProviderID, provisioner, infra.ProviderConfig{
			Name:        cfg.providerName,
			Description: cfg.providerDescription,
			Icon:        base64.RawStdEncoding.EncodeToString(icon),
			Schema:      schema,
		})
		if err != nil {
			return fmt.Errorf("failed to create infra provider: %w", err)
		}

		logger.Info("starting infra provider",
			zap.String("provider_id", meta.ProviderID),
			zap.String("omni_endpoint", cfg.omniAPIEndpoint))

		clientOptions := []client.Option{
			client.WithInsecureSkipTLSVerify(cfg.insecureSkipVerify),
		}

		if cfg.serviceAccountKey != "" {
			clientOptions = append(clientOptions, client.WithServiceAccount(cfg.serviceAccountKey))
		}

		return ip.Run(
			cmd.Context(),
			logger,
			infra.WithOmniEndpoint(cfg.omniAPIEndpoint),
			infra.WithClientOptions(clientOptions...),
			infra.WithEncodeRequestIDsIntoTokens(),
		)
	},
}

var cfg struct {
	omniAPIEndpoint     string
	serviceAccountKey   string
	providerName        string
	providerDescription string
	configFile          string
	insecureSkipVerify  bool
}

func main() {
	if err := app(); err != nil {
		os.Exit(1)
	}
}

func app() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer cancel()

	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.Flags().StringVar(&cfg.omniAPIEndpoint, "omni-api-endpoint", os.Getenv("OMNI_ENDPOINT"),
		"endpoint of the Omni API; defaults to $OMNI_ENDPOINT")
	rootCmd.Flags().StringVar(&meta.ProviderID, "id", meta.ProviderID,
		"infra provider id; matches resources via the infra provider label")
	rootCmd.Flags().StringVar(&cfg.serviceAccountKey, "omni-service-account-key", os.Getenv("OMNI_SERVICE_ACCOUNT_KEY"),
		"Omni service account key; defaults to $OMNI_SERVICE_ACCOUNT_KEY")
	rootCmd.Flags().StringVar(&cfg.providerName, "provider-name", "OVHcloud",
		"provider name as it appears in Omni")
	rootCmd.Flags().StringVar(&cfg.providerDescription, "provider-description", "OVHcloud Public Cloud (OpenStack) infrastructure provider",
		"provider description as it appears in Omni")
	rootCmd.Flags().BoolVar(&cfg.insecureSkipVerify, "insecure-skip-verify", false,
		"ignore untrusted certs on the Omni side")
	rootCmd.Flags().StringVar(&cfg.configFile, "config-file", "",
		"path to the YAML provider config file (OS_* env vars also accepted)")
}
