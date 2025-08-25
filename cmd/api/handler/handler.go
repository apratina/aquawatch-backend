package handler

import (
	"aquawatch/internal"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
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

	// Record a "started" status for this site in DynamoDB (best-effort).
	if err := internal.AddPredictionTrackerStarted(ctx, stationID); err != nil {
		log.Printf("ddbv2: failed to write prediction-tracker started item: %v", err)
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

// PredictionStatusHandler queries the prediction-tracker table by site and status
// and returns whether the prediction is considered in-progress (created within 5 minutes).
func PredictionStatusHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	site := r.URL.Query().Get("site")
	if site == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing site"})
		return
	}
	statusParam := r.URL.Query().Get("status")
	if statusParam == "" {
		statusParam = "started"
	}

	item, err := internal.GetPredictionTrackerItem(ctx, site, statusParam)
	if err != nil {
		log.Printf("ddb: get item failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to query status"})
		return
	}

	inProgress := false
	var createdOn, updatedOn int64
	if item != nil {
		createdOn = item.CreatedOn
		updatedOn = item.UpdatedOn
		ageMs := time.Now().UTC().UnixMilli() - item.CreatedOn
		inProgress = ageMs < 5*60*1000
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"site":         site,
		"status":       statusParam,
		"in_progress":  inProgress,
		"createdon_ms": createdOn,
		"updatedon_ms": updatedOn,
	})
}

// SubscribeAlertsHandler subscribes an email to the alerts SNS topic.
// Accepts POST with JSON body: {"email": "user@example.com"}
func SubscribeAlertsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	// Basic email validation regex
	pattern := regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	if !pattern.MatchString(strings.TrimSpace(req.Email)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}

	ctx := r.Context()
	arn, err := internal.SubscribeAlertsEmail(ctx, strings.TrimSpace(req.Email))
	if err != nil {
		if err == internal.ErrAlreadySubscribed {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already subscribed"})
			return
		}
		log.Printf("sns subscribe failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "subscription failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":          "subscription requested; check email to confirm",
		"subscription_arn": arn,
	})
}
