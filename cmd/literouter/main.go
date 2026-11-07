package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imMamdouhaboammar/liteRouter/pkg/circuitbreaker"
	"github.com/imMamdouhaboammar/liteRouter/pkg/decision"
	"github.com/imMamdouhaboammar/liteRouter/pkg/gateway"
	"github.com/imMamdouhaboammar/liteRouter/pkg/kvstore"
	"github.com/imMamdouhaboammar/liteRouter/pkg/spark"
)

func main() {
	port := flag.Int("port", 8080, "Port for liteRouter HTTP/SSE Gateway")
	nodeID := flag.String("node-id", "literouter-edge-01", "Unique cluster node identifier")
	policyFile := flag.String("policy", "rules/default_routing.star", "Path to Starlark routing policy")
	flag.Parse()

	log.Printf("[liteRouter] Initializing Node: %s on port :%d\n", *nodeID, *port)

	// 1. Initialize Decentralized Link-State KvStore
	kv := kvstore.NewKvStore(*nodeID, 500*time.Millisecond)
	defer kv.Close()

	// 2. Register Upstream AI Providers & Models
	bootstrapProvidersAndModels(kv)

	// 3. Initialize AI Spark Health Engine
	sparkEngine := spark.NewSparkEngine(kv, spark.NewHTTPProber(3*time.Second), 2*time.Second)
	registerSparkTargets(sparkEngine)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sparkEngine.Start(ctx)
	defer sparkEngine.Stop()

	// 4. Initialize Mathematical Decision Capability Engine
	decisionEng := decision.NewEngine()
	if *policyFile != "" {
		if content, err := os.ReadFile(*policyFile); err == nil {
			decisionEng.SetPolicyScript(string(content))
			log.Printf("[liteRouter] Loaded Starlark dynamic routing policy from %s\n", *policyFile)
		}
	}

	// 5. Initialize Adaptive Circuit Breaker & Failover Coordinator
	cbManager := circuitbreaker.NewManager()
	failover := circuitbreaker.NewFailoverCoordinator(cbManager)

	// 6. Bootstrap High-Concurrency Gateway
	gwServer := gateway.NewServer(kv, decisionEng, failover)
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      gwServer.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Handle Graceful Termination
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("[liteRouter] Shutting down gracefully...")
		cancel()
		shutdownCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("[liteRouter] Ready to accept OpenAI-compatible requests at http://localhost:%d/v1/chat/completions\n", *port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[liteRouter] Fatal server error: %v\n", err)
	}
}

func bootstrapProvidersAndModels(kv *kvstore.KvStore) {
	// Provider 1: OpenAI
	kv.Registry().RegisterProvider(kvstore.Provider{
		ID:             "openai-direct",
		Name:           "OpenAI Global API",
		BaseURL:        "https://api.openai.com/v1",
		APIKeyEnv:      "OPENAI_API_KEY",
		Status:         kvstore.StatusHealthy,
		MaxConcurrency: 1000,
	})

	// Model: gpt-4o
	kv.Registry().RegisterModel(kvstore.Model{
		ID:         "gpt-4o",
		ProviderID: "openai-direct",
		Capabilities: kvstore.CapabilityScores{
			Reasoning:          4.8,
			Code:               4.8,
			Math:               4.7,
			Creative:           4.9,
			ContextWindow:      128000,
			LatencySensitivity: 4.5,
		},
		Pricing: kvstore.Pricing{
			PromptPerMillionTokens:     2.50,
			CompletionPerMillionTokens: 10.00,
		},
		HealthScore: 1.0,
		Active:      true,
	})

	// Provider 2: DeepSeek
	kv.Registry().RegisterProvider(kvstore.Provider{
		ID:             "deepseek-api",
		Name:           "DeepSeek Cloud API",
		BaseURL:        "https://api.deepseek.com/v1",
		APIKeyEnv:      "DEEPSEEK_API_KEY",
		Status:         kvstore.StatusHealthy,
		MaxConcurrency: 800,
	})

	// Model: deepseek-v3
	kv.Registry().RegisterModel(kvstore.Model{
		ID:         "deepseek-v3",
		ProviderID: "deepseek-api",
		Capabilities: kvstore.CapabilityScores{
			Reasoning:          4.7,
			Code:               4.9,
			Math:               4.8,
			Creative:           4.2,
			ContextWindow:      64000,
			LatencySensitivity: 4.0,
		},
		Pricing: kvstore.Pricing{
			PromptPerMillionTokens:     0.14,
			CompletionPerMillionTokens: 0.28,
		},
		HealthScore: 1.0,
		Active:      true,
	})

	// Provider 3: Anthropic
	kv.Registry().RegisterProvider(kvstore.Provider{
		ID:             "anthropic-direct",
		Name:           "Anthropic Claude API",
		BaseURL:        "https://api.anthropic.com/v1",
		APIKeyEnv:      "ANTHROPIC_API_KEY",
		Status:         kvstore.StatusHealthy,
		MaxConcurrency: 600,
	})

	// Model: claude-3-5-sonnet
	kv.Registry().RegisterModel(kvstore.Model{
		ID:         "claude-3-5-sonnet",
		ProviderID: "anthropic-direct",
		Capabilities: kvstore.CapabilityScores{
			Reasoning:          4.9,
			Code:               5.0,
			Math:               4.8,
			Creative:           4.9,
			ContextWindow:      200000,
			LatencySensitivity: 4.2,
		},
		Pricing: kvstore.Pricing{
			PromptPerMillionTokens:     3.00,
			CompletionPerMillionTokens: 15.00,
		},
		HealthScore: 1.0,
		Active:      true,
	})
}

func registerSparkTargets(sparkEngine *spark.SparkEngine) {
	sparkEngine.RegisterTarget(spark.ProviderTarget{
		ProviderID: "openai-direct",
		ModelID:    "gpt-4o",
		BaseURL:    "https://api.openai.com/v1",
	}, kvstore.Pricing{PromptPerMillionTokens: 2.50})

	sparkEngine.RegisterTarget(spark.ProviderTarget{
		ProviderID: "deepseek-api",
		ModelID:    "deepseek-v3",
		BaseURL:    "https://api.deepseek.com/v1",
	}, kvstore.Pricing{PromptPerMillionTokens: 0.14})

	sparkEngine.RegisterTarget(spark.ProviderTarget{
		ProviderID: "anthropic-direct",
		ModelID:    "claude-3-5-sonnet",
		BaseURL:    "https://api.anthropic.com/v1",
	}, kvstore.Pricing{PromptPerMillionTokens: 3.00})
}
