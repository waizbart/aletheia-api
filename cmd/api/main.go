package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/attestation"
	"github.com/waizbart/aletheia-api/internal/config"
	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/observability"
	"github.com/waizbart/aletheia-api/internal/repository"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading environment directly")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("postgres", config.MustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	log.Println("connected to PostgreSQL")

	extractor := feature.NewOpenCVExtractor()
	defer extractor.Close()

	certRepo := repository.NewPostgresCertificateRepo(db)
	anchorRepo := repository.NewPostgresAnchorRepo(db)
	anchorSvc, err := repository.NewAnchorServiceFromEnv()
	if err != nil {
		log.Fatalf("initializing anchor service: %v", err)
	}
	log.Printf("anchoring from %s", anchorSvc.From())

	// Observability: OpenTelemetry tracer (no-op unless an OTLP endpoint is set)
	// plus the in-memory collector that backs the live dashboard.
	otelEndpoint := config.EnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	serviceName := config.EnvOrDefault("OTEL_SERVICE_NAME", "aletheia-api")
	shutdownTracer, tracer, err := observability.InitTracerProvider(ctx, otelEndpoint, serviceName)
	if err != nil {
		log.Fatalf("initializing tracer: %v", err)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracer(shCtx); err != nil {
			log.Printf("tracer shutdown: %v", err)
		}
	}()
	if otelEndpoint != "" {
		log.Printf("OpenTelemetry traces exporting to %s", otelEndpoint)
	}

	ringCap, _ := strconv.Atoi(config.EnvOrDefault("OBS_RING_CAPACITY", "50"))
	collector := observability.NewCollector(ringCap)
	obsFactory := observability.NewFactory(collector, tracer)

	deviceRepo := repository.NewPostgresDeviceRepo(db)
	nonceRepo := repository.NewPostgresNonceRepo(db)
	orgRepo := repository.NewPostgresOrgRepo(db)
	usageRepo := repository.NewPostgresUsageRepo(db)

	attestations, err := attestation.NewRegistryFromEnv()
	if err != nil {
		log.Fatalf("initializing attestation verifiers: %v", err)
	}
	log.Printf("attestation enabled for %v", attestations.Platforms())

	certifyUC := usecase.NewCertifyUseCase(certRepo, extractor)
	verifyUC := usecase.NewVerifyUseCase(certRepo, extractor)
	deleteUC := usecase.NewDeleteUseCase(certRepo)
	thumbnailUC := usecase.NewThumbnailUseCase(certRepo, extractor)

	nonceTTL := config.EnvDurationOrDefault("CAPTURE_NONCE_TTL", 5*time.Minute)
	issueNonceUC := usecase.NewIssueNonceUseCase(nonceRepo, nonceTTL, time.Now)
	enrollUC := usecase.NewEnrollDeviceUseCase(deviceRepo, nonceRepo, attestations, time.Now)
	revokeDeviceUC := usecase.NewRevokeDeviceUseCase(deviceRepo, time.Now)
	captureUC := usecase.NewAttestedCaptureUseCase(deviceRepo, nonceRepo, certifyUC, time.Now)

	anchorUC := usecase.NewAnchorUseCase(
		anchorRepo, anchorSvc,
		config.EnvIntOrDefault("ANCHOR_BATCH_SIZE", 4096),
		time.Now,
	)
	go anchorUC.Run(ctx, config.EnvDurationOrDefault("ANCHOR_INTERVAL", time.Hour))

	usageUC := usecase.NewUsageUseCase(usageRepo, time.Now)
	createOrgUC := usecase.NewCreateOrgUseCase(orgRepo, time.Now)
	issueKeyUC := usecase.NewIssueAPIKeyUseCase(orgRepo, time.Now)
	revokeKeyUC := usecase.NewRevokeAPIKeyUseCase(orgRepo, time.Now)
	authUC := usecase.NewAuthenticateUseCase(orgRepo)

	adminToken := config.EnvOrDefault("ADMIN_API_TOKEN", "")
	if adminToken == "" {
		log.Println("WARNING: ADMIN_API_TOKEN is unset — admin routes will reject every request")
	}
	admin := handler.AdminAuth(adminToken)
	tenant := handler.APIKeyAuth(authUC)
	optionalTenant := handler.OptionalAPIKeyAuth(authUC)

	allowUnattested := config.EnvBoolOrDefault("ALLOW_UNATTESTED_CERTIFY", false)
	if allowUnattested {
		log.Println("WARNING: ALLOW_UNATTESTED_CERTIFY is on — POST /certificates accepts uploads with no capture-time provenance")
	}

	certHandler := handler.NewCertificateHandler(certifyUC, verifyUC, deleteUC, usageUC, allowUnattested)
	captureHandler := handler.NewCaptureHandler(issueNonceUC, enrollUC, revokeDeviceUC, captureUC, usageUC, usageUC)
	adminHandler := handler.NewAdminHandler(createOrgUC, issueKeyUC, revokeKeyUC)

	mux := http.NewServeMux()
	certHandler.RegisterRoutes(mux, admin, tenant)
	captureHandler.RegisterRoutes(mux, tenant)
	adminHandler.RegisterRoutes(mux, admin)
	handler.RegisterDocsRoutes(mux)
	handler.RegisterHealthRoutes(mux)
	handler.RegisterObservabilityRoutes(mux, collector, thumbnailUC, admin)

	corsOrigins := strings.Split(config.EnvOrDefault("CORS_ALLOWED_ORIGINS", "*"), ",")
	limiter := handler.NewRateLimiter(
		config.EnvIntOrDefault("RATE_LIMIT_RPS", 20),
		config.EnvIntOrDefault("RATE_LIMIT_BURST", 40),
	)
	trustProxyHeaders := config.EnvBoolOrDefault("TRUST_PROXY_HEADERS", false)

	// Order matters: logging sees every request, CORS answers preflights before
	// they consume rate-limit budget, and the concurrency cap sits closest to
	// the mux so it bounds only work that actually reaches a handler.
	wrapped := handler.LoggingMiddleware(
		handler.CORS(corsOrigins)(
			handler.RateLimit(limiter, trustProxyHeaders)(
				handler.ConcurrencyLimit(config.EnvIntOrDefault("MAX_CONCURRENT_REQUESTS", 32))(
					optionalTenant(
						handler.ObservabilityMiddleware(obsFactory)(mux))))))

	port := config.EnvOrDefault("SERVER_PORT", "8080")
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: wrapped,
		// ReadTimeout and WriteTimeout stay unset on purpose: uploads run to
		// 100 MB and the dashboard holds SSE connections open indefinitely, so
		// a blanket deadline would break both. Slowloris is covered by
		// ReadHeaderTimeout, and idle connections by IdleTimeout.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("server listening on %s", srv.Addr)
		log.Printf("observability dashboard at http://localhost:%s/observability", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down…")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}
