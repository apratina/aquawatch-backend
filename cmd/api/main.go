package main

import (
	"aquawatch/cmd/api/handler"
	"aquawatch/internal"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// withCORS wraps an http.Handler to add permissive CORS headers and handle preflight requests.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		allowed := r.Header.Get("Access-Control-Request-Headers")
		if allowed == "" {
			allowed = "Content-Type, Authorization, X-Requested-With, Accept, Origin, X-Verify-Request-Id, X-Verify-Code, X-Session-Token"
		}
		w.Header().Set("Access-Control-Allow-Headers", allowed)
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
	mux.HandleFunc("/sms/send", handler.SendSMSCodeHandler)
	mux.HandleFunc("/sms/verify", handler.VerifySMSCodeHandler)
	mux.HandleFunc("/report/pdf", handler.GenerateReportPDFHandler)
	mux.HandleFunc("/alerts", handler.ListAlertsHandler)

	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}

	// Vonage Verify middleware (skips /healthz and OPTIONS)
	flag := os.Getenv("VONAGE_VERIFY_ENABLED")
	useVonage := true
	if flag != "" {
		switch strings.ToLower(flag) {
		case "false", "0", "no", "off":
			useVonage = false
		}
	}
	authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		// Allow unauthenticated access to SMS start/verify endpoints
		if r.URL.Path == "/sms/send" || r.URL.Path == "/sms/verify" {
			mux.ServeHTTP(w, r)
			return
		}
		// Accept either Vonage verify headers or an app session token
		if tok := r.Header.Get("X-Session-Token"); tok != "" {
			if _, err := internal.ValidateSessionToken(tok); err == nil {
				mux.ServeHTTP(w, r)
				return
			}
		}
		if useVonage {
			reqID := r.Header.Get("X-Verify-Request-Id")
			code := r.Header.Get("X-Verify-Code")
			if reqID == "" || code == "" {
				http.Error(w, "missing verification headers", http.StatusUnauthorized)
				return
			}
			ok, err := internal.VerifyCheck(r.Context(), reqID, code)
			if err != nil || !ok {
				http.Error(w, "verification failed", http.StatusUnauthorized)
				return
			}
		} else {
			// Vonage disabled: allow through without additional auth
			mux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	log.Printf("Starting AquaWatch API on :%s", addr)
	if err := http.ListenAndServe(":"+addr, withLogging(withCORS(authenticated))); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// withLogging logs request method, path, status, duration, and bytes written.
type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if lrw.status == 0 {
		lrw.status = http.StatusOK
	}
	n, err := lrw.ResponseWriter.Write(b)
	lrw.bytes += n
	return n, err
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lrw, r)
		dur := time.Since(start)
		log.Printf("%s %s %d %dB %s ua=%q", r.Method, r.URL.Path, lrw.status, lrw.bytes, dur, r.UserAgent())
	})
}
