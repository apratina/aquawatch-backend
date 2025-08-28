package handler

import (
	"aquawatch/internal"
	"encoding/base64"
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

// reportPDFRequest represents the JSON body for generating the PDF report.
type reportPDFRequest struct {
	ImageBase64 string                `json:"image_base64"`
	Items       []internal.ReportItem `json:"items"`
}

// anomalyRequest represents inputs from the frontend for the anomaly check.
type anomalyRequest struct {
	Sites     []string `json:"sites"`
	MinLat    float64  `json:"min_lat"`
	MinLng    float64  `json:"min_lng"`
	MaxLat    float64  `json:"max_lat"`
	MaxLng    float64  `json:"max_lng"`
	Threshold float64  `json:"threshold_percent"`
	Parameter string   `json:"parameter"`
}

type anomalyItem struct {
	Site            string  `json:"site"`
	S3Key           string  `json:"s3_key"`
	ObservedValue   float64 `json:"observed_value"`
	PredictedValue  float64 `json:"predicted_value"`
	PercentChange   float64 `json:"percent_change"`
	Anomalous       bool    `json:"anomalous"`
	AnomalousReason string  `json:"anomalous_reason"`
}

type anomalyResponse struct {
	Items []anomalyItem `json:"items"`
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

	// Accept multiple stations: repeated station params and/or comma-separated 'stations'
	var stationIDs []string
	if vals, ok := r.URL.Query()["station"]; ok {
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v != "" {
				stationIDs = append(stationIDs, v)
			}
		}
	}
	if s := strings.TrimSpace(r.URL.Query().Get("stations")); s != "" {
		parts := strings.Split(s, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				stationIDs = append(stationIDs, p)
			}
		}
	}
	if len(stationIDs) == 0 {
		stationIDs = []string{"03339000"}
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

	processedKey := fmt.Sprintf("processed/%d.csv", time.Now().UTC().Unix())

	input := map[string]any{
		"station":      stationIDs,
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
		inProgress = ageMs < 15*60*1000
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

// SendSMSCodeHandler starts a Vonage Verify request (SMS) for a phone number.
// POST {"phone_e164":"+15551234567","brand":"AquaWatch"} -> {"session_id":"<request_id>"}
func SendSMSCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		PhoneE164 string `json:"phone_e164"`
		Brand     string `json:"brand"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.PhoneE164) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	requestID, err := internal.VerifyStart(r.Context(), strings.TrimSpace(req.PhoneE164), strings.TrimSpace(req.Brand))
	if err != nil {
		log.Printf("verify start failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to send code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session_id": requestID})
}

// VerifySMSCodeHandler checks the Vonage code and mints a session token on success.
// POST {"session_id":"<request_id>","code":"123456"} -> {"token":"..."}
func VerifySMSCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Code      string `json:"code"`
		PhoneE164 string `json:"phone_e164"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" || strings.TrimSpace(req.Code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	ok, err := internal.VerifyCheck(r.Context(), req.SessionID, strings.TrimSpace(req.Code))
	if err != nil || !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
		return
	}
	// Mint a short-lived session token (default 12h)
	ttl := 12 * time.Hour
	if v := os.Getenv("SESSION_TTL_HOURS"); v != "" {
		if d, err := time.ParseDuration(v + "h"); err == nil {
			ttl = d
		}
	}
	// Use provided phone if available; otherwise bind to empty string
	phone := strings.TrimSpace(req.PhoneE164)
	token, err := internal.MintSessionToken(phone, ttl)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mint token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// GenerateReportPDFHandler accepts an image (base64) and table items, generates a PDF, uploads to S3, and returns the S3 key.
// POST {"image_base64":"...","items":[{"site":"...","reason":"...","predicted_value":1.2,"anomaly_date":"2025-01-01"}]}
func GenerateReportPDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req reportPDFRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.ImageBase64) == "" || len(req.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image and items required"})
		return
	}
	imgBytes, err := decodeBase64Image(req.ImageBase64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid image"})
		return
	}

	pdfBytes, err := internal.GenerateReportPDF(r.Context(), imgBytes, req.Items)
	if err != nil {
		log.Printf("pdf generation failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "pdf generation failed"})
		return
	}
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "S3_BUCKET not configured"})
		return
	}
	key := fmt.Sprintf("reports/%d.pdf", time.Now().UTC().UnixNano())
	if err := internal.SaveToS3WithKey(r.Context(), pdfBytes, bucket, key); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to upload pdf"})
		return
	}
	url, err := internal.GeneratePresignedGetURL(r.Context(), bucket, key, 5*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"s3_key": key})
		return
	}

	// Best-effort: write alert tracker record
	_ = internal.SaveAlertTrackerRecord(r.Context(), map[string]any{
		"gsi_pk":         "recent",
		"createdon":      time.Now().UTC().UnixMilli(),
		"alert_id":       fmt.Sprintf("alert-%d", time.Now().UnixMilli()),
		"alert_name":     "Anomaly Report",
		"s3_signed_url":  url,
		"severity":       "high",
		"sites_impacted": collectSitesFromItems(req.Items),
		"anomaly_date":   guessAnomalyDate(req.Items),
	})

	writeJSON(w, http.StatusOK, map[string]string{"s3_key": key, "url": url})
}

func decodeBase64Image(s string) ([]byte, error) {
	// Strip potential data URL prefix
	if i := strings.Index(s, ","); i >= 0 {
		s = s[i+1:]
	}
	return base64.StdEncoding.DecodeString(s)
}

func collectSitesFromItems(items []internal.ReportItem) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		s := strings.TrimSpace(it.Site)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func guessAnomalyDate(items []internal.ReportItem) string {
	for _, it := range items {
		if strings.TrimSpace(it.AnomalyDate) != "" {
			return it.AnomalyDate
		}
	}
	return time.Now().UTC().Format(time.RFC3339)
}

// AnomalyCheckHandler accepts a site and bounding box and performs
// fetch->preprocess->infer->anomaly detection using a configured threshold.
// POST JSON body: {"site":"03339000","min_lat":..,"min_lng":..,"max_lat":..,"max_lng":..,"threshold_percent":10}
func AnomalyCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req anomalyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	sites := req.Sites
	if len(sites) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing sites"})
		return
	}
	if len(sites) > 10 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many sites (max 10)"})
		return
	}
	threshold := req.Threshold
	if threshold <= 0 {
		// default 10%
		threshold = 10
	}
	parameter := req.Parameter
	if parameter == "" {
		parameter = "00060"
	}

	items := make([]anomalyItem, 0, len(sites))
	for _, site := range sites {
		site = strings.TrimSpace(site)
		if site == "" {
			continue
		}
		res, err := internal.ProcessInferAndDetect(r.Context(), site, parameter, threshold)
		if err != nil {
			log.Printf("anomaly flow failed for site %s: %v", site, err)
			continue
		}
		var anomalousReason string
		if res.Anomalous {
			anomalousReason = "high discharge"
		}
		items = append(items, anomalyItem{
			Site:            site,
			S3Key:           res.S3Key,
			ObservedValue:   res.ObservedValue,
			PredictedValue:  res.PredictedValue,
			PercentChange:   res.PercentChange,
			Anomalous:       res.Anomalous,
			AnomalousReason: anomalousReason,
		})
	}

	// Best-effort: publish one SNS alert covering all anomalous sites
	{
		var count int
		var b strings.Builder
		for _, it := range items {
			if it.Anomalous {
				count++
				fmt.Fprintf(&b, "Site %s anomalous: observed=%.2f predicted=%.2f (%.1f%%)\n", it.Site, it.ObservedValue, it.PredictedValue, it.PercentChange)
			}
		}
		if count > 0 {
			subject := fmt.Sprintf("AquaWatch Anomalies Detected (%d)", count)
			_ = internal.PublishAlert(r.Context(), subject, b.String())
		}
	}
	writeJSON(w, http.StatusOK, anomalyResponse{Items: items})
}

// ListAlertsHandler returns alerts from the last N minutes (default 10).
// GET /alerts?minutes=10
func ListAlertsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("minutes")
	minutes := 10
	if strings.TrimSpace(q) != "" {
		var v int
		if _, err := fmt.Sscanf(q, "%d", &v); err == nil && v > 0 && v <= 1440 {
			minutes = v
		}
	}
	since := time.Now().UTC().Add(-time.Duration(minutes) * time.Minute).UnixMilli()
	items, err := internal.ListRecentAlerts(r.Context(), since, 200)
	if err != nil {
		log.Printf("failed to list alerts: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to list alerts"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": items, "since_ms": since})
}

// ListTrainModelsHandler returns training records from the last N minutes (default 60) in descending order.
// GET /train/models?minutes=60
func ListTrainModelsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("minutes")
	minutes := 60
	if strings.TrimSpace(q) != "" {
		var v int
		if _, err := fmt.Sscanf(q, "%d", &v); err == nil && v > 0 && v <= 10080 { // up to 7 days
			minutes = v
		}
	}
	since := time.Now().UTC().Add(-time.Duration(minutes) * time.Minute).UnixMilli()
	items, err := internal.ListRecentTrainModels(r.Context(), since, 200)
	if err != nil {
		log.Printf("failed to list train models: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to list train models"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "since_ms": since})
}
