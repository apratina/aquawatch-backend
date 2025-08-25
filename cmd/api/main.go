package main

import (
	"aquawatch/cmd/api/handler"
	"log"
	"net/http"
	"os"
)

// withCORS wraps an http.Handler to add permissive CORS headers and handle preflight requests.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, Accept, Origin")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.HealthHandler)
	mux.HandleFunc("/ingest", handler.IngestHandler)
	mux.HandleFunc("/prediction/status", handler.PredictionStatusHandler)
	mux.HandleFunc("/alerts/subscribe", handler.SubscribeAlertsHandler)
	mux.HandleFunc("/anomaly/check", handler.AnomalyCheckHandler)

	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}

	log.Printf("Starting AquaWatch API on :%s", addr)
	if err := http.ListenAndServe(":"+addr, withCORS(mux)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
