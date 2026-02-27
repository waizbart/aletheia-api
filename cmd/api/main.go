package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/config"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/repository"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading environment directly")
	}

	db, err := sql.Open("postgres", config.MustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	log.Println("connected to PostgreSQL")

	certRepo := repository.NewPostgresCertificateRepo(db)
	chainSvc := repository.NewStubBlockchainService()

	certifyUC := usecase.NewCertifyUseCase(certRepo, chainSvc)
	verifyUC := usecase.NewVerifyUseCase(certRepo)

	certHandler := handler.NewCertificateHandler(certifyUC, verifyUC)

	mux := http.NewServeMux()
	certHandler.RegisterRoutes(mux)
	handler.RegisterDocsRoutes(mux)
	handler.RegisterHealthRoutes(mux)

	wrapped := handler.LoggingMiddleware(mux)

	port := config.EnvOrDefault("SERVER_PORT", "8080")
	addr := fmt.Sprintf(":%s", port)
	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, wrapped); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
