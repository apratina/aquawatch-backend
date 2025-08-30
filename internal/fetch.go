package internal

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// USGSResponse is a minimal placeholder for potential parsing of the USGS
// service response. The ingest flow currently forwards raw payloads to
// preprocessing, so this type is intentionally lightweight.
type USGSResponse struct {
	Value struct {
		Time struct {
			// Example format: 2025-08-23T12:00Z
			Time  string `json:"time"`
			Value string `json:"value"`
		} `json:"value"`
	} `json:"value"`
}

// GetWaterDataBatch fetches USGS Instantaneous Values for each station id in the slice
// and returns one raw JSON payload per station, in the same order.
// parameter example: "00060" (discharge), "00065" (gage height)
func GetWaterDataBatch(stationIDs []string, parameter string) ([][]byte, error) {
	results := make([][]byte, 0, len(stationIDs))
	for _, stationID := range stationIDs {
		stationID = strings.TrimSpace(stationID)
		log.Println("get water data for stationID", stationID)
		if stationID == "" {
			results = append(results, nil)
			continue
		}
		url := fmt.Sprintf(
			"https://waterservices.usgs.gov/nwis/iv/?format=json&sites=%s&parameterCd=%s",
			stationID,
			parameter,
		)
		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("USGS API request failed for %s: %w", stationID, err)
		}
		if resp.Body != nil {
			defer resp.Body.Close()
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("USGS API non-OK status for %s: %d", stationID, resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading HTTP response failed for %s: %w", stationID, err)
		}
		results = append(results, data)
	}
	return results, nil
}

// GetWaterData is a compatibility wrapper for fetching a single station's payload.
func GetWaterData(stationID string, parameter string) ([]byte, error) {
	payloads, err := GetWaterDataBatch([]string{stationID}, parameter)
	if err != nil {
		return nil, err
	}
	if len(payloads) == 0 || payloads[0] == nil {
		return nil, fmt.Errorf("no data returned for station %s", stationID)
	}
	return payloads[0], nil
}

// GetWaterDailyDataLast30DaysBatch fetches USGS Daily Values (mean by default) for the
// last 30 days for each station id and returns one raw JSON payload per station.
// Uses the DV endpoint with statCd=00003 (mean).
func GetWaterDailyDataLast30DaysBatch(stationIDs []string, parameter string) ([][]byte, error) {
	results := make([][]byte, 0, len(stationIDs))
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -30)
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	for _, stationID := range stationIDs {
		stationID = strings.TrimSpace(stationID)
		log.Println("get daily water data (30d) for stationID", stationID)
		if stationID == "" {
			results = append(results, nil)
			continue
		}
		url := fmt.Sprintf(
			"https://waterservices.usgs.gov/nwis/dv/?format=json&sites=%s&parameterCd=%s&statCd=00003&startDT=%s&endDT=%s",
			stationID,
			parameter,
			startStr,
			endStr,
		)
		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("USGS DV API request failed for %s: %w", stationID, err)
		}
		if resp.Body != nil {
			defer resp.Body.Close()
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("USGS DV API non-OK status for %s: %d", stationID, resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading DV HTTP response failed for %s: %w", stationID, err)
		}
		results = append(results, data)
	}
	return results, nil
}
