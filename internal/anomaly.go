package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	minPredictedValue       = 15
	defaultThresholdPercent = 20
)

// AnomalyResult encapsulates the outcome of processing, inference, and anomaly detection.
type AnomalyResult struct {
	S3Key          string  `json:"s3_key"`
	ObservedValue  float64 `json:"observed_value"`
	PredictedValue float64 `json:"predicted_value"`
	PercentChange  float64 `json:"percent_change"`
	Anomalous      bool    `json:"anomalous"`
}

// parseLatestObserved extracts the most recent observed value from USGS JSON.
func parseLatestObserved(raw []byte) (float64, error) {
	var usgs USGSJSON
	if err := json.Unmarshal(raw, &usgs); err != nil {
		return 0, err
	}
	for _, ts := range usgs.Value.TimeSeries {
		// Iterate values to find latest timestamp
		var latestTime time.Time
		var latestVal float64
		found := false
		for _, vv := range ts.Values {
			for _, p := range vv.Value {
				t, err := parseUSGSTime(p.DateTime)
				if err != nil {
					continue
				}
				var v float64
				_, _ = fmt.Sscanf(p.Value, "%f", &v)
				if !found || t.After(latestTime) {
					found = true
					latestTime = t
					latestVal = v
				}
			}
		}
		if found {
			return latestVal, nil
		}
	}
	return 0, errors.New("no observations found")
}

// parsePredictions attempts to parse numeric predictions from the model output.
// It accepts CSV-like or newline-delimited numbers and returns the last value.
func parsePredictions(output []byte) (float64, error) {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return 0, errors.New("empty prediction output")
	}
	// Remove surrounding brackets if present, e.g., "[66]" -> "66"
	text = strings.TrimPrefix(text, "[")
	text = strings.TrimSuffix(text, "]")
	// Split by newlines and commas to capture most simple formats
	seps := []string{"\n", "\r", ",", "\t", " "}
	for _, sep := range seps {
		text = strings.ReplaceAll(text, sep, ",")
	}
	parts := strings.Split(text, ",")
	var last float64
	found := false
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			continue
		}
		last = v
		found = true
	}
	if !found {
		return 0, errors.New("no numeric predictions parsed")
	}
	return last, nil
}

// ProcessInferAndDetect executes the flow: fetch -> preprocess CSV -> store -> infer -> detect anomaly.
// thresholdPercent is a percentage (e.g., 10 means 10%).
func ProcessInferAndDetect(ctx context.Context, stationID, parameter string) (*AnomalyResult, error) {
	if stationID == "" {
		return nil, errors.New("station id required")
	}
	if parameter == "" {
		parameter = "00060"
	}

	raw, err := GetWaterDataBatch([]string{stationID}, parameter)
	if err != nil {
		return nil, err
	}

	observed, err := parseLatestObserved(raw[0])
	if err != nil {
		return nil, err
	}

	csvBytes, err := PreprocessDataCSV(ctx, raw[0])
	if err != nil {
		return nil, err
	}

	bucket := os.Getenv("S3_BUCKET")
	key := fmt.Sprintf("processed/%s/%d.csv", stationID, time.Now().UTC().Unix())
	if bucket != "" {
		_ = SaveToS3WithKey(ctx, csvBytes, bucket, key)
	}

	endpoint := os.Getenv("SAGEMAKER_ENDPOINT")
	if endpoint == "" {
		return nil, errors.New("SAGEMAKER_ENDPOINT not configured")
	}
	targetModel := os.Getenv("DEFAULT_MODEL")
	if targetModel == "" {
		return nil, errors.New("DEFAULT_MODEL not configured")
	}

	// Convert label+features CSV to features-only payload for inference
	// We avoid importing encoding/csv here to minimize diff; a simple split is sufficient
	lines := strings.Split(strings.TrimSpace(string(csvBytes)), "\n")
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) > 1 {
			cols = cols[1:]
		}
		b.WriteString(strings.Join(cols, ","))
		b.WriteByte('\n')
	}

	predOut, err := InvokeEndpoint(ctx, endpoint, []byte(b.String()), targetModel)
	if err != nil {
		return nil, err
	}
	log.Println("for station", stationID, "predOut", string(predOut))
	predicted, err := parsePredictions(predOut)
	if err != nil {
		return nil, err
	}

	// Round observed and predicted to 2 decimal places for response consistency
	obsRounded := math.Round(observed*100) / 100
	predRounded := math.Round(predicted*100) / 100

	den := math.Max(1e-9, math.Abs(observed))
	percent := math.Abs(predicted-observed) / den * 100.0
	anom := percent > defaultThresholdPercent && predicted > minPredictedValue

	return &AnomalyResult{
		S3Key:          key,
		ObservedValue:  obsRounded,
		PredictedValue: predRounded,
		PercentChange:  percent,
		Anomalous:      anom,
	}, nil
}
