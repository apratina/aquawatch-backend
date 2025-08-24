package internal

import (
	"fmt"
	"io"
	"net/http"
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

// GetWaterData issues a simple HTTP GET against the USGS Instantaneous Values
// service for the provided station and parameter code (e.g., 00060 = discharge,
// 00065 = gage height). It returns the raw JSON bytes as delivered by the API.
func GetWaterData(stationID, parameter string) ([]byte, error) {
	// Replace 'parameter' with "00060" for streamflow or "00065" for gage height
	url := fmt.Sprintf(
		"https://waterservices.usgs.gov/nwis/iv/?format=json&sites=%s&parameterCd=%s",
		stationID,
		parameter,
	)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("USGS API request failed: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("USGS API non-OK status: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading HTTP response failed: %w", err)
	}

	// Caller performs any domain-specific validation/parsing.
	return data, nil
}
