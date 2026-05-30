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

	certifyUC := usecase.NewCertifyUseCase(certRepo, chainSvc, extractor, blobStore)
	verifyUC := usecase.NewVerifyUseCase(certRepo, extractor, blobStore)
	deleteUC := usecase.NewDeleteUseCase(certRepo, blobStore)

	certHandler := handler.NewCertificateHandler(certifyUC, verifyUC, deleteUC)

	mux := http.NewServeMux()
	certHandler.RegisterRoutes(mux)
	handler.RegisterDocsRoutes(mux)
	handler.RegisterHealthRoutes(mux)
	handler.RegisterObservabilityRoutes(mux, collector, blobStore)

	corsOrigins := strings.Split(config.EnvOrDefault("CORS_ALLOWED_ORIGINS", "*"), ",")
	wrapped := handler.LoggingMiddleware(handler.CORS(corsOrigins)(handler.ObservabilityMiddleware(obsFactory)(mux)))

	port := config.EnvOrDefault("SERVER_PORT", "8080")
	srv := &http.Server{Addr: fmt.Sprintf(":%s", port), Handler: wrapped}

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
