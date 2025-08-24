package handler

import (
	"aquawatch/internal"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// ingestResponse is returned by the API after starting the Step Functions run.
type ingestResponse struct {
	Message      string `json:"message"`
	S3Key        string `json:"s3_key,omitempty"`
	Bytes        int    `json:"bytes,omitempty"`
	Timestamp    string `json:"timestamp"`
	ExecutionArn string `json:"execution_arn,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// IngestHandler starts the ingestion workflow by launching the Step Functions
// pipeline. It supports optional `train` query param to skip training.
func IngestHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.Println("AquaWatch Ingest API called")

	stateMachineArn := os.Getenv("STATE_MACHINE_ARN")
	if stateMachineArn == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "STATE_MACHINE_ARN not configured"})
		return
	}

	stationID := r.URL.Query().Get("station")
	if stationID == "" {
		stationID = "03339000"
	}
	parameter := r.URL.Query().Get("parameter")
	if parameter == "" {
		parameter = "00060"
	}

	// Optional training flag (default false unless train=true)
	trainParam := r.URL.Query().Get("train")
	trainFlag := false
	if trainParam != "" {
		switch strings.ToLower(trainParam) {
		case "true", "1", "yes":
			trainFlag = true
		}
	}

	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "S3_BUCKET not configured"})
		return
	}

	processedKey := "processed/latest.csv"

	input := map[string]any{
		"station":      stationID,
		"parameter":    parameter,
		"bucket":       bucket,
		"processedKey": processedKey,
		"train":        trainFlag,
	}
	execArn, err := internal.StartStateMachine(ctx, stateMachineArn, input)
	if err != nil {
		log.Printf("start state machine failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("state machine start failed: %v", err)})
		return
	}

	writeJSON(w, http.StatusOK, ingestResponse{
		Message:      "execution started",
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		ExecutionArn: execArn,
	})
}

// HealthHandler returns a basic OK response.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
