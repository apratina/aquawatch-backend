package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// This file contains small helpers to query the National Weather Service
// (api.weather.gov) for a location's short-term forecast.

type nwsPointsResponse struct {
	Properties struct {
		Forecast string `json:"forecast"`
	} `json:"properties"`
}

type nwsForecastResponse struct {
	Properties struct {
		Periods []struct {
			Temperature     int    `json:"temperature"`
			TemperatureUnit string `json:"temperatureUnit"`
			WindSpeed       string `json:"windSpeed"`
			WindDirection   string `json:"windDirection"`
		} `json:"periods"`
	} `json:"properties"`
}

// FetchWeatherForecast requests the forecast URL for the given coordinates and
// returns the first forecast period's temperature (and unit) along with wind
// speed and direction. If the API is unavailable, the caller should treat the
// returned error and decide on a fallback policy.
func FetchWeatherForecast(lat, lon float64) (int, string, string, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	pointsURL := fmt.Sprintf("https://api.weather.gov/points/%0.4f,%0.4f", lat, lon)

	req, err := http.NewRequest(http.MethodGet, pointsURL, nil)
	if err != nil {
		return 0, "", "", "", err
	}
	req.Header.Set("User-Agent", "aquawatch/1.0 (contact: dev@aquawatch)")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", "", "", fmt.Errorf("points request failed: %d", resp.StatusCode)
	}
	var pr nwsPointsResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return 0, "", "", "", err
	}
	if pr.Properties.Forecast == "" {
		return 0, "", "", "", fmt.Errorf("forecast URL missing in response")
	}

	freq, err := http.NewRequest(http.MethodGet, pr.Properties.Forecast, nil)
	if err != nil {
		return 0, "", "", "", err
	}
	freq.Header.Set("User-Agent", "aquawatch/1.0 (contact: dev@aquawatch)")
	fresp, err := client.Do(freq)
	if err != nil {
		return 0, "", "", "", err
	}
	defer fresp.Body.Close()
	if fresp.StatusCode != http.StatusOK {
		return 0, "", "", "", fmt.Errorf("forecast request failed: %d", fresp.StatusCode)
	}
	var fr nwsForecastResponse
	if err := json.NewDecoder(fresp.Body).Decode(&fr); err != nil {
		return 0, "", "", "", err
	}
	if len(fr.Properties.Periods) == 0 {
		return 0, "", "", "", fmt.Errorf("no forecast periods available")
	}
	p := fr.Properties.Periods[0]
	return p.Temperature, p.TemperatureUnit, p.WindSpeed, p.WindDirection, nil
}
