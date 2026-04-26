package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MemaxLabs/memax-go-agent-sdk/modelregistry"
)

const (
	defaultModelRegistryMaxAge  = 24 * time.Hour
	defaultModelRegistryTimeout = 900 * time.Millisecond
)

func hydrateModelRegistry(ctx context.Context, opts *options) {
	if opts == nil || opts.ContextWindow > 0 || strings.TrimSpace(opts.Model) == "" {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("MEMAX_CODE_MODEL_REGISTRY")))
	if mode == "off" || mode == "false" || mode == "0" {
		return
	}
	cachePath := strings.TrimSpace(os.Getenv("MEMAX_CODE_MODEL_REGISTRY_CACHE"))
	if cachePath == "" {
		cachePath = defaultModelRegistryCachePath()
	}
	if resolved, err := resolvePath(cachePath); err == nil {
		cachePath = resolved
	}
	url := strings.TrimSpace(os.Getenv("MEMAX_CODE_MODEL_REGISTRY_URL"))
	if url == "" {
		url = modelregistry.ModelsDevURL
	}
	timeout := defaultModelRegistryTimeout
	if raw := strings.TrimSpace(os.Getenv("MEMAX_CODE_MODEL_REGISTRY_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	maxAge := defaultModelRegistryMaxAge
	if raw := strings.TrimSpace(os.Getenv("MEMAX_CODE_MODEL_REGISTRY_MAX_AGE")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
			maxAge = parsed
		}
	}
	loadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	registry, result, err := modelregistry.Load(loadCtx, modelregistry.LoadOptions{
		URL:       url,
		CachePath: cachePath,
		MaxAge:    maxAge,
	})
	if err != nil || registry == nil {
		return
	}
	caps, ok := registry.LookupCapabilities(string(opts.Provider), opts.Model)
	if !ok || caps.ContextWindowTokens <= 0 {
		opts.ModelRegistryInfo = fmt.Sprintf("%s no_match", result.Source)
		return
	}
	opts.ModelCapabilities = caps
	if result.Stale {
		opts.ModelRegistryInfo = fmt.Sprintf("%s stale models.dev", result.Source)
	} else {
		opts.ModelRegistryInfo = fmt.Sprintf("%s models.dev", result.Source)
	}
}
