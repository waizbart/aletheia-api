package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/anchor"
	"github.com/waizbart/aletheia-api/internal/config"
	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/handler"
	dbmigrate "github.com/waizbart/aletheia-api/internal/migrate"
	"github.com/waizbart/aletheia-api/internal/observability"
	"github.com/waizbart/aletheia-api/internal/repository"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading environment directly")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Apply schema migrations on startup (works on existing databases, unlike the
	// old initdb-only approach).
	if err := dbmigrate.Run(cfg.DatabaseURL); err != nil {
		log.Fatalf("running migrations: %v", err)
	}
	log.Println("database migrations up to date")

	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	log.Println("connected to PostgreSQL")

	blobStore, err := repository.NewS3BlobStoreFromEnv(ctx)
	if err != nil {
		log.Fatalf("initializing blob store: %v", err)
	}
	log.Println("connected to S3-compatible blob store")

	extractor := feature.NewOpenCVExtractor()
	defer extractor.Close()

	certRepo := repository.NewPostgresCertificateRepo(db)
	chainSvc, err := repository.NewBlockchainServiceFromEnv()
	if err != nil {
		log.Fatalf("initializing blockchain service: %v", err)
	}

	// Observability: OpenTelemetry tracer (no-op unless an OTLP endpoint is set)
	// plus the in-memory collector that backs the live dashboard.
	shutdownTracer, tracer, err := observability.InitTracerProvider(ctx, cfg.OTELEndpoint, cfg.OTELServiceName)
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
	if cfg.OTELEndpoint != "" {
		log.Printf("OpenTelemetry traces exporting to %s", cfg.OTELEndpoint)
	}

	collector := observability.NewCollector(cfg.ObsRingCapacity)
	obsFactory := observability.NewFactory(collector, tracer)

	certifyUC := usecase.NewCertifyUseCase(certRepo, extractor, blobStore)
	verifyUC := usecase.NewVerifyUseCase(certRepo, extractor, blobStore)
	deleteUC := usecase.NewDeleteUseCase(certRepo, blobStore)

	// Background anchor worker drains the pending-anchor outbox asynchronously.
	worker := anchor.NewWorker(certRepo, chainSvc, cfg.AnchorWorkerInterval, cfg.AnchorWorkerBatch, cfg.AnchorMaxAttempts)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = worker.Run(ctx)
	}()
	log.Printf("anchor worker started (interval=%s batch=%d maxAttempts=%d)",
		cfg.AnchorWorkerInterval, cfg.AnchorWorkerBatch, cfg.AnchorMaxAttempts)

	certHandler := handler.NewCertificateHandler(certifyUC, verifyUC, deleteUC)

	mux := http.NewServeMux()
	certHandler.RegisterRoutes(mux)
	handler.RegisterDocsRoutes(mux)
	handler.RegisterHealthRoutes(mux)
	handler.RegisterObservabilityRoutes(mux, collector, blobStore)

	rateLimiter := handler.NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst)

	// Outermost first: log every request, answer CORS, rate-limit, authenticate,
	// then trace and dispatch. Rate limiting precedes auth so floods of invalid
	// keys are throttled by IP.
	wrapped := handler.LoggingMiddleware(
		handler.CORS(cfg.CORSAllowedOrigins)(
			rateLimiter.Middleware(
				handler.APIKeyAuth(cfg.APIKeys)(
					handler.ObservabilityMiddleware(obsFactory)(mux),
				),
			),
		),
	)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.ServerPort),
		Handler:           wrapped,
		ReadTimeout:       cfg.HTTPReadTimeout,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}

	go func() {
		log.Printf("server listening on %s", srv.Addr)
		log.Printf("observability dashboard at http://localhost:%s/observability", cfg.ServerPort)
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
	// ctx is already cancelled (signal), so the worker is winding down; wait for
	// its in-flight batch to finish before the deferred db.Close runs.
	wg.Wait()
}
