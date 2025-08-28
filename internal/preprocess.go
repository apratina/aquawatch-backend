package internal

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Define the simplified structure for processed water data
type ProcessedData struct {
	StationID string    `json:"station_id"`
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Unit      string    `json:"unit"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
}

// USGS JSON structure (simplified for real-time data)
type USGSJSON struct {
	Value struct {
		TimeSeries []struct {
			SourceInfo struct {
				SiteName string `json:"siteName"`
				SiteCode []struct {
					Value      string `json:"value"`
					Network    string `json:"network"`
					AgencyCode string `json:"agencyCode"`
				} `json:"siteCode"`
				TimeZoneInfo struct {
					DefaultTimeZone struct {
						ZoneOffset       string `json:"zoneOffset"`
						ZoneAbbreviation string `json:"zoneAbbreviation"`
					} `json:"defaultTimeZone"`
					DaylightSavingsTimeZone struct {
						ZoneOffset       string `json:"zoneOffset"`
						ZoneAbbreviation string `json:"zoneAbbreviation"`
					} `json:"daylightSavingsTimeZone"`
					SiteUsesDaylightSavingsTime bool `json:"siteUsesDaylightSavingsTime"`
				} `json:"timeZoneInfo"`
				GeoLocation struct {
					GeogLocation struct {
						Srs       string  `json:"srs"`
						Latitude  float64 `json:"latitude"`
						Longitude float64 `json:"longitude"`
					} `json:"geogLocation"`
					LocalSiteXY []interface{} `json:"localSiteXY"`
				} `json:"geoLocation"`
				Note         []interface{} `json:"note"`
				SiteType     []interface{} `json:"siteType"`
				SiteProperty []struct {
					Value string `json:"value"`
					Name  string `json:"name"`
				} `json:"siteProperty"`
			} `json:"sourceInfo"`
			Variable struct {
				VariableCode []struct {
					Value      string `json:"value"`
					Network    string `json:"network"`
					Vocabulary string `json:"vocabulary"`
					VariableID int    `json:"variableID"`
					Default    bool   `json:"default"`
				} `json:"variableCode"`
				VariableName        string `json:"variableName"`
				VariableDescription string `json:"variableDescription"`
				ValueType           string `json:"valueType"`
				Unit                struct {
					UnitCode string `json:"unitCode"`
				} `json:"unit"`
				Options struct {
					Option []struct {
						Name       string `json:"name"`
						OptionCode string `json:"optionCode"`
					} `json:"option"`
				} `json:"options"`
				Note             []interface{} `json:"note"`
				NoDataValue      float64       `json:"noDataValue"`
				VariableProperty []interface{} `json:"variableProperty"`
				Oid              string        `json:"oid"`
			} `json:"variable"`
			Values []struct {
				Value []struct {
					Value      string   `json:"value"`
					Qualifiers []string `json:"qualifiers"`
					DateTime   string   `json:"dateTime"`
				} `json:"value"`
				Qualifier []struct {
					QualifierCode        string `json:"qualifierCode"`
					QualifierDescription string `json:"qualifierDescription"`
					QualifierID          int    `json:"qualifierID"`
					Network              string `json:"network"`
					Vocabulary           string `json:"vocabulary"`
				} `json:"qualifier"`
				QualityControlLevel []interface{} `json:"qualityControlLevel"`
				Method              []struct {
					MethodDescription string `json:"methodDescription"`
					MethodID          int    `json:"methodID"`
				} `json:"method"`
				Source     []interface{} `json:"source"`
				Offset     []interface{} `json:"offset"`
				Sample     []interface{} `json:"sample"`
				CensorCode []interface{} `json:"censorCode"`
			} `json:"values"`
			Name string `json:"name"`
		} `json:"timeSeries"`
	} `json:"value"`
}

// PreprocessData parses raw USGS JSON and converts it into structured ProcessedData
func PreprocessData(ctx context.Context, rawData []byte) ([]byte, error) {
	var usgs USGSJSON
	var processed []ProcessedData

	err := json.Unmarshal(rawData, &usgs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse USGS JSON: %w", err)
	}

	for _, ts := range usgs.Value.TimeSeries {
		stationID := ts.SourceInfo.SiteCode[0].Value
		unit := ts.Variable.Unit.UnitCode
		lat := ts.SourceInfo.GeoLocation.GeogLocation.Latitude
		lng := ts.SourceInfo.GeoLocation.GeogLocation.Longitude
		for _, v := range ts.Values {
			for _, point := range v.Value {
				t, err := time.Parse(time.RFC3339, point.DateTime)
				if err != nil {
					continue
				}
				var value float64
				fmt.Sscanf(point.Value, "%f", &value)
				processed = append(processed, ProcessedData{
					StationID: stationID,
					Timestamp: t,
					Value:     value,
					Unit:      unit,
					Latitude:  lat,
					Longitude: lng,
				})
			}
		}
	}

	// Marshal processed data back to JSON for storage
	output, err := json.Marshal(processed)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal processed data: %w", err)
	}

	return output, nil
}

// PreprocessDataCSV parses raw USGS JSON and returns CSV bytes without header.
// Numeric Columns only (label then features): value,timestamp_unix,latitude,longitude,wx_temp
func PreprocessDataCSV(ctx context.Context, rawData []byte) ([]byte, error) {
	var usgs USGSJSON
	if err := json.Unmarshal(rawData, &usgs); err != nil {
		return nil, fmt.Errorf("failed to parse USGS JSON: %w", err)
	}

	buf := &bytes.Buffer{}
	writer := csv.NewWriter(buf)

	for _, ts := range usgs.Value.TimeSeries {
		lat := ts.SourceInfo.GeoLocation.GeogLocation.Latitude
		lng := ts.SourceInfo.GeoLocation.GeogLocation.Longitude
		// stationID := ts.SourceInfo.SiteCode[0].Value

		// fetch weather once per time series (constant for all points here)
		temp, _, _, _, wxErr := FetchWeatherForecast(lat, lng)
		if wxErr != nil {
			// fallback to zero if weather fetch fails
			temp = 0
		}

		for _, v := range ts.Values {
			for _, point := range v.Value {
				t, err := time.Parse(time.RFC3339, point.DateTime)
				if err != nil {
					continue
				}
				var value float64
				fmt.Sscanf(point.Value, "%f", &value)
				record := []string{
					fmt.Sprintf("%f", value),
					// fmt.Sprintf("%s", stationID),
					fmt.Sprintf("%d", t.Unix()),
					fmt.Sprintf("%f", lat),
					fmt.Sprintf("%f", lng),
					fmt.Sprintf("%d", temp),
				}
				if err := writer.Write(record); err != nil {
					return nil, fmt.Errorf("failed writing csv: %w", err)
				}
			}
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("csv writer error: %w", err)
	}

	return buf.Bytes(), nil
}

// PreprocessDataCSVBatch takes multiple raw USGS JSON payloads and concatenates their
// CSV feature rows (no header). Each payload should be a standalone USGS JSON document.
func PreprocessDataCSVBatch(ctx context.Context, rawPayloads [][]byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	for i, p := range rawPayloads {
		log.Println("PreprocessDataCSVBatch - input", i, string(p))
		if len(p) == 0 {
			continue
		}
		b, err := PreprocessDataCSV(ctx, p)
		if err != nil {
			return nil, err
		}
		if buf.Len() > 0 && buf.Bytes()[buf.Len()-1] != '\n' {
			buf.WriteByte('\n')
		}
		log.Println("PreprocessDataCSVBatch - output", i, string(b))
		buf.Write(b)
	}
	return buf.Bytes(), nil
}
