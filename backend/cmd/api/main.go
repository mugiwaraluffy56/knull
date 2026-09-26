// Command api is the Knull incident API server.
//
// It loads configuration, connects to PostgreSQL and Redis, applies database
// migrations, and serves the HTTP API (health endpoints in this foundation
// slice) until it receives an interrupt, then shuts down gracefully.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mugiwaraluffy56/knull/backend/internal/actions"
	"github.com/mugiwaraluffy56/knull/backend/internal/alerts"
	"github.com/mugiwaraluffy56/knull/backend/internal/approval"
	"github.com/mugiwaraluffy56/knull/backend/internal/auth"
	"github.com/mugiwaraluffy56/knull/backend/internal/classify"
	"github.com/mugiwaraluffy56/knull/backend/internal/config"
	"github.com/mugiwaraluffy56/knull/backend/internal/executor"
	"github.com/mugiwaraluffy56/knull/backend/internal/httpapi"
	"github.com/mugiwaraluffy56/knull/backend/internal/incidents"
	"github.com/mugiwaraluffy56/knull/backend/internal/investigate"
	"github.com/mugiwaraluffy56/knull/backend/internal/mcp"
	"github.com/mugiwaraluffy56/knull/backend/internal/operational"
	"github.com/mugiwaraluffy56/knull/backend/internal/operators"
	"github.com/mugiwaraluffy56/knull/backend/internal/recovery"
	"github.com/mugiwaraluffy56/knull/backend/internal/recoverypolicy"
	"github.com/mugiwaraluffy56/knull/backend/internal/respond"
	"github.com/mugiwaraluffy56/knull/backend/internal/sandbox"
	"github.com/mugiwaraluffy56/knull/backend/internal/secrets"
	"github.com/mugiwaraluffy56/knull/backend/internal/services"
	"github.com/mugiwaraluffy56/knull/backend/internal/store"
	"github.com/mugiwaraluffy56/knull/backend/internal/validation"
	"github.com/mugiwaraluffy56/knull/backend/internal/workflow"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer st.Close()
	logger.Info("connected to dependencies")

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	logger.Info("migrations applied")

	sessions := auth.NewSessionManager(st.Redis, cfg.SessionTTL, cfg.CookieSecure)
	operatorStore := operators.NewStore(st.Pool)
	serviceStore := services.NewStore(st.Pool)
	incidentStore := incidents.NewStore(st.Pool)
	alertFailures := alerts.NewFailureStore(st.Pool)
	monitor := &operational.Monitor{Deps: st, Incidents: incidentStore, Endpoints: map[string]operational.Endpoint{
		"trueforge":      {URL: strings.TrimRight(cfg.TrueForgeURL, "/") + "/api/v1/models", Token: cfg.TrueForgeToken},
		"kubernetes-mcp": {URL: cfg.K8sMCPURL, Token: cfg.K8sMCPToken},
		"prometheus-mcp": {URL: cfg.PrometheusMCPURL, Token: cfg.PrometheusMCPToken},
		"github-mcp":     {URL: cfg.GitHubMCPURL, Token: cfg.GitHubMCPToken},
		"jev":            {URL: cfg.JevMCPURL},
	}}
	if cfg.TrueForgeURL == "" {
		monitor.Endpoints["trueforge"] = operational.Endpoint{}
	}
	if cfg.SandboxKubeconfig != "" {
		monitor.Sandbox = sandbox.Kubectl{Kubeconfig: cfg.SandboxKubeconfig, Context: cfg.SandboxContext}
		monitor.ExpectedSandboxUID = cfg.SandboxClusterUID
	}
	alertIntake := alerts.NewIntake(incidentStore, serviceStore, alertFailures, alerts.LabelMapping{
		ServiceLabel:     cfg.AlertServiceLabel,
		EnvironmentLabel: cfg.AlertEnvironmentLabel,
	})

	var workflowManager *workflow.Manager
	if cfg.TrueForgeURL != "" {
		runtime := workflow.NewHTTPRuntime(cfg.TrueForgeURL, cfg.TrueForgeToken)
		workflowManager = workflow.NewManager(runtime, incidentStore, logger)
		alertIntake.SetWorkflow(workflowManager)
		logger.Info("trueforge workflow enabled", "url", cfg.TrueForgeURL)
		if err := workflowManager.ResumeAll(ctx); err != nil {
			logger.Error("resume workflows", "error", err)
		}
	} else {
		logger.Warn("KNULL_TRUEFORGE_URL not set; durable investigations are disabled")
	}

	var k8sInvestigator *investigate.KubernetesInvestigator
	if cfg.K8sMCPURL != "" {
		k8sClient := mcp.NewClient(mcp.NewHTTPTransport(cfg.K8sMCPURL, cfg.K8sMCPToken))
		k8sInvestigator = investigate.NewKubernetesInvestigator(k8sClient)
		logger.Info("kubernetes mcp investigation enabled", "url", cfg.K8sMCPURL)
	} else {
		logger.Warn("KNULL_K8S_MCP_URL not set; kubernetes investigation is disabled")
	}
	var prometheusInvestigator *investigate.PrometheusInvestigator
	if cfg.PrometheusMCPURL != "" {
		prometheusClient := mcp.NewClient(mcp.NewHTTPTransport(cfg.PrometheusMCPURL, cfg.PrometheusMCPToken))
		prometheusInvestigator = investigate.NewPrometheusInvestigator(prometheusClient)
		logger.Info("prometheus mcp investigation enabled", "url", cfg.PrometheusMCPURL)
	} else {
		logger.Warn("KNULL_PROMETHEUS_MCP_URL not set; prometheus investigation is disabled")
	}
	var githubInvestigator *investigate.GitHubInvestigator
	if cfg.GitHubMCPURL != "" {
		githubClient := mcp.NewClient(mcp.NewHTTPTransport(cfg.GitHubMCPURL, cfg.GitHubMCPToken))
		githubInvestigator = investigate.NewGitHubInvestigator(githubClient)
		logger.Info("github mcp investigation enabled", "url", cfg.GitHubMCPURL)
	} else {
		logger.Warn("KNULL_GITHUB_MCP_URL not set; github investigation is disabled")
	}
	var collector *investigate.Collector
	if k8sInvestigator != nil || prometheusInvestigator != nil || githubInvestigator != nil {
		collector = investigate.NewCollector(incidentStore, serviceStore, k8sInvestigator, prometheusInvestigator)
		collector.WithGitHub(githubInvestigator)
	}
	var classifier *classify.Service
	if cfg.JevMCPURL != "" {
		jevClient := &classify.MCPClient{Endpoint: cfg.JevMCPURL}
		classifier = classify.NewService(incidentStore, jevClient, cfg.JevMinConfidence)
		classifier.SetNext(respond.NewService(incidentStore, jevClient, cfg.JevMinConfidence))
		logger.Info("Jev classification enabled", "url", cfg.JevMCPURL)
	}
	var sandboxService *sandbox.Service
	var memoryValidator *validation.Service
	if cfg.SandboxKubeconfig != "" {
		client := sandbox.Kubectl{Kubeconfig: cfg.SandboxKubeconfig, Context: cfg.SandboxContext}
		runner := sandbox.NewRunner(client, sandbox.Config{ClusterUID: cfg.SandboxClusterUID, ProductionClusterUIDs: cfg.ProductionClusterUIDs, AllowedImageRegistry: cfg.SandboxImageRegistry, PullSecretName: cfg.SandboxPullSecretName})
		sandboxService = sandbox.NewService(incidentStore, runner)
		logger.Info("sandbox runner configured", "cluster_uid", cfg.SandboxClusterUID)
		if cfg.TrueForgeURL != "" && cfg.TrueForgeValidationModel != "" && cfg.SandboxCodeImageDigest != "" && cfg.SandboxPrometheusURL != "" && cfg.SandboxErrorRateQuery != "" && cfg.SandboxP95Query != "" {
			codeExecutor := validation.NewKubernetesCodeExecutor(validation.EKSExecutorConfig{Kubeconfig: cfg.SandboxKubeconfig, Context: cfg.SandboxContext, ClusterUID: cfg.SandboxClusterUID, ImageDigest: cfg.SandboxCodeImageDigest, AllowedImageRegistry: cfg.SandboxImageRegistry})
			script := validation.NewTrueForgeScriptEngine(validation.TrueForgeConfig{BaseURL: cfg.TrueForgeURL, Token: cfg.TrueForgeToken, Model: cfg.TrueForgeValidationModel, Executor: codeExecutor})
			metrics := validation.PrometheusObserver{BaseURL: cfg.SandboxPrometheusURL, Token: cfg.SandboxPrometheusToken, ErrorRateQuery: cfg.SandboxErrorRateQuery, P95MillisecondsQuery: cfg.SandboxP95Query}
			memoryValidator = validation.NewService(incidentStore, runner, script, validation.KubernetesPodObserver{Reader: client}, metrics)
			logger.Info("memory validation configured")
		}
	}

	var cipher *secrets.Cipher
	if cfg.SecretKey != "" {
		cipher, err = secrets.NewCipher(cfg.SecretKey)
		if err != nil {
			return err
		}
	} else {
		logger.Warn("KNULL_SECRET_KEY not set; storing integration credentials is disabled")
	}
	secretStore := secrets.NewStore(st.Pool, cipher)
	approvalService := approval.NewService(st.Pool)
	recoveryPolicies := recoverypolicy.NewStore(st.Pool)
	var productionExecutor *executor.Service
	var recoveryService *recovery.Service
	if cfg.ProductionExecutionEnabled {
		client, err := executor.InCluster(cfg.ProductionCluster, cfg.ProductionNamespace, cfg.ProductionWorkload, cfg.ProductionCASHA256)
		if err != nil {
			return err
		}
		productionExecutor = &executor.Service{Gate: approvalService, Client: client, History: incidentStore}
		logger.Info("production memory executor enabled", "cluster", cfg.ProductionCluster, "namespace", cfg.ProductionNamespace, "workload", cfg.ProductionWorkload)
		if cfg.JevMCPURL != "" && cfg.ProductionPrometheusURL != "" && cfg.ProductionErrorRateQuery != "" && cfg.ProductionP95Query != "" {
			recoveryService = &recovery.Service{History: incidentStore, Policies: recoveryPolicies, Observer: recovery.LiveObserver{Kubernetes: client.Client, Cluster: cfg.ProductionCluster, Namespace: cfg.ProductionNamespace, Workload: cfg.ProductionWorkload, PrometheusURL: cfg.ProductionPrometheusURL, PrometheusToken: cfg.ProductionPrometheusToken, ErrorRateQuery: cfg.ProductionErrorRateQuery, LatencyP95Query: cfg.ProductionP95Query}, Jev: &classify.MCPClient{Endpoint: cfg.JevMCPURL}}
		}
	}

	var authn *auth.Authenticator
	if cfg.OIDC.Configured() {
		redirectURL := cfg.AppBaseURL + "/api/auth/callback"
		authn, err = auth.NewAuthenticator(ctx, cfg.OIDC.Issuer, cfg.OIDC.ClientID, cfg.OIDC.ClientSecret, redirectURL)
		if err != nil {
			// A missing identity provider must not crash the whole service; it
			// degrades to health-only. Protected endpoints reject requests.
			logger.Error("oidc initialization failed; sign-in disabled", "error", err)
		} else {
			logger.Info("oidc sign-in enabled", "issuer", cfg.OIDC.Issuer)
		}
	} else {
		logger.Warn("OIDC not configured; operator sign-in is disabled")
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.New(httpapi.Options{
			Deps:              st,
			AllowedOrigin:     cfg.AllowedOrigin,
			HealthTimeout:     cfg.HealthTimeout,
			Sessions:          sessions,
			Authn:             authn,
			Operators:         operatorStore,
			Secrets:           secretStore,
			Services:          serviceStore,
			RecoveryPolicies:  recoveryPolicies,
			RecoveryVerifier:  recoveryService,
			Resolutions:       incidentStore,
			IntegrationHealth: monitor,
			Incidents:         incidentStore,
			FleetIncidents:    incidentStore,
			Workflow:          workflowStarterOrNil(workflowManager),
			Collector:         collectorOrNil(collector),
			Classifier:        classifier,
			ActionPlanner:     actions.NewService(incidentStore, serviceStore),
			Approvals:         approvalService,
			Executor:          productionExecutor,
			Sandbox:           sandboxService,
			MemoryValidator:   memoryValidator,
			AlertIntake:       alertIntake,
			AlertFailures:     alertFailures,
			AlertSecret:       cfg.AlertmanagerSecret,
			AppBaseURL:        cfg.AppBaseURL,
			UIBaseURL:         cfg.UIBaseURL,
			Logger:            logger,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("server stopped cleanly")
	return nil
}

// workflowStarterOrNil returns a nil interface when no manager is configured, so
// the HTTP layer correctly treats the workflow as absent rather than holding a
// non-nil interface wrapping a nil pointer.
func workflowStarterOrNil(m *workflow.Manager) httpapi.WorkflowStarter {
	if m == nil {
		return nil
	}
	return m
}

// collectorOrNil returns a nil interface when no investigator is configured.
func collectorOrNil(c *investigate.Collector) httpapi.EvidenceCollector {
	if c == nil {
		return nil
	}
	return c
}
