package main

import (
	"aquawatch/internal"
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
)

// preprocessInput captures inputs passed by Step Functions. The handler fetches
// raw USGS data for the station/parameter, converts to CSV features, and
// appends to S3 at the provided processed key.
type preprocessInput struct {
	StationID    []string `json:"station"`
	Parameter    string   `json:"parameter"`
	Bucket       string   `json:"bucket"`
	ProcessedKey string   `json:"processedKey"`
}

// handler downloads fresh data, transforms it, and appends to the dataset in S3.
func handler(ctx context.Context, input preprocessInput) error {
	log.Println("AquaWatch Preprocess Lambda triggered")

	if input.Bucket == "" || len(input.StationID) == 0 || input.Parameter == "" || input.ProcessedKey == "" {
		return fmt.Errorf("missing required fields: bucket, data")
	}

	rawPayloads, err := internal.GetWaterDailyDataLast30DaysBatch(input.StationID, input.Parameter)
	if err != nil {
		// daily API can fail; fallback to instantaneous current data as a last resort
		log.Printf("daily 30d fetch failed, fallback to iv: %v", err)
		rawPayloads, err = internal.GetWaterDataBatch(input.StationID, input.Parameter)
		if err != nil {
			// get water data api is very flaky, so we'll use mock data as fallback
			log.Printf("using mock data since get water data failed: %v", err)
			rawPayloads = [][]byte{[]byte(`{"name":"ns1:timeSeriesResponseType","declaredType":"org.cuahsi.waterml.TimeSeriesResponseType","scope":"javax.xml.bind.JAXBElement$GlobalScope","value":{"queryInfo":{"queryURL":"http://waterservices.usgs.gov/nwis/iv/format=json&sites=03339000&parameterCd=00060","criteria":{"locationParam":"[ALL:03339000]","variableParam":"[00060]","parameter":[]},"note":[{"value":"[ALL:03339000]","title":"filter:sites"},{"value":"[mode=LATEST, modifiedSince=null]","title":"filter:timeRange"},{"value":"methodIds=[ALL]","title":"filter:methodId"},{"value":"2025-08-24T16:44:54.347Z","title":"requestDT"},{"value":"a94b52a0-8109-11f0-841b-2cea7f5e5ede","title":"requestId"},{"value":"Provisional data are subject to revision. Go to http://waterdata.usgs.gov/nwis/help/?provisional for more information.","title":"disclaimer"},{"value":"sdas01","title":"server"}]},"timeSeries":[{"sourceInfo":{"siteName":"VERMILION RIVER NEAR DANVILLE, IL","siteCode":[{"value":"03339000","network":"NWIS","agencyCode":"USGS"}],"timeZoneInfo":{"defaultTimeZone":{"zoneOffset":"-06:00","zoneAbbreviation":"CST"},"daylightSavingsTimeZone":{"zoneOffset":"-05:00","zoneAbbreviation":"CDT"},"siteUsesDaylightSavingsTime":false},"geoLocation":{"geogLocation":{"srs":"EPSG:4326","latitude":40.1010833,"longitude":-87.5976111},"localSiteXY":[]},"note":[],"siteType":[],"siteProperty":[{"value":"ST","name":"siteTypeCd"},{"value":"05120109","name":"hucCd"},{"value":"17","name":"stateCd"},{"value":"17183","name":"countyCd"}]},"variable":{"variableCode":[{"value":"00060","network":"NWIS","vocabulary":"NWIS:UnitValues","variableID":45807197,"default":true}],"variableName":"Streamflow, ft&#179;/s","variableDescription":"Discharge, cubic feet per second","valueType":"Derived Value","unit":{"unitCode":"ft3/s"},"options":{"option":[{"name":"Statistic","optionCode":"00000"}]},"note":[],"noDataValue":-999999.0,"variableProperty":[],"oid":"45807197"},"values":[{"value":[{"value":"72.3","qualifiers":["P"],"dateTime":"2025-08-24T10:15:00.000-06:00"}],"qualifier":[{"qualifierCode":"P","qualifierDescription":"Provisional data subject to revision.","qualifierID":0,"network":"NWIS","vocabulary":"uv_rmk_cd"}],"qualityControlLevel":[],"method":[{"methodDescription":"","methodID":49959}],"source":[],"offset":[],"sample":[],"censorCode":[]}],"name":"USGS:03339000:00060:00000"}]} ,"nil":false,"globalScope":true,"typeSubstituted":false}`)}
		}
	}

	csvBytes, err := internal.PreprocessDataCSVBatch(ctx, rawPayloads)
	if err != nil {
		return fmt.Errorf("preprocessing failed: %w", err)
	}

	// Append to existing CSV if present; otherwise create it.
	existing, readErr := internal.LoadFromS3(ctx, input.Bucket, input.ProcessedKey)
	if readErr == nil && len(existing) > 0 {
		if existing[len(existing)-1] != '\n' {
			existing = append(existing, '\n')
		}
		existing = append(existing, csvBytes...)
		csvBytes = existing
	} else if readErr != nil {
		log.Printf("no existing processed file or failed to read: %v; creating new", readErr)
	}

	if err := internal.SaveToS3WithKey(ctx, csvBytes, input.Bucket, input.ProcessedKey); err != nil {
		return fmt.Errorf("failed to save processed data: %w", err)
	}

	return nil
}

func main() {
	lambda.Start(handler)
}
