package main

import (
	"aquawatch/cmd/api/handler"
	"log"
	"net/http"
	"os"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.HealthHandler)
	mux.HandleFunc("/ingest", handler.IngestHandler)

	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}

	log.Printf("Starting AquaWatch API on :%s", addr)
	if err := http.ListenAndServe(":"+addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
