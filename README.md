# AquaWatch

AquaWatch is a small, end-to-end pipeline that:

- Fetches real-time USGS water data for a station/parameter
- Preprocesses the data into a numeric CSV dataset and enriches it with NOAA weather context
- Optionally trains an XGBoost model via AWS Step Functions (synchronous)
- Invokes a SageMaker endpoint to generate predictions
- Exposes a simple HTTP API to trigger the pipeline

The project is written in Go and designed to run on AWS (Lambda, S3, Step Functions, SageMaker).

## Repository layout

- `cmd/api/` – HTTP API server entrypoint and handlers
- `internal/` – shared helpers (USGS fetch, preprocessing, weather, storage, inference)
- `lambdas/` – Lambda handlers (`preprocess`, `infer`)
- `infra/state_machine/` – Step Functions definition (`aquawatch.json`)
- `scripts/` – deployment helpers (`install.sh`)

## Quick start

Prerequisites:
- Go 1.21+
- AWS CLI v2 with credentials
- An S3 bucket for datasets
- A SageMaker endpoint (single or multi-model) for inference

Environment variables (examples):

```bash
export ACCOUNT_ID=123456789012
export AWS_REGION=us-west-2
export S3_BUCKET=your-aquawatch-bucket
export SAGEMAKER_ENDPOINT=aquawatch-xgb
# Optional for MME; if you maintain a default model key
export DEFAULT_MODEL=s3://your-aquawatch-bucket/model/your-model/output/model.tar.gz
```

Deploy Lambda functions and Step Functions (renders placeholders in the state machine):

```bash
./scripts/install.sh
```

Run the API locally (for testing):

```bash
PORT=8080 S3_BUCKET=$S3_BUCKET STATE_MACHINE_ARN=arn:aws:states:$AWS_REGION:$ACCOUNT_ID:stateMachine:aquawatch-pipeline \
  go run ./cmd/api
```

Trigger ingestion:

```bash
curl "http://localhost:8080/ingest?station=03339000&parameter=00060&train=false"
```

## Data & features

Preprocessing produces numeric-only CSV rows in the order:

```
value,timestamp_unix,latitude,longitude,wx_temp
```

- `value` is the label (e.g., streamflow)
- `timestamp_unix`, `latitude`, `longitude` are features
- `wx_temp` is current forecast temperature (from NOAA), used as a numeric feature

Additional context is available through `internal/preprocess.go` and `internal/weather.go`.

## State machine

The Step Functions definition at `infra/state_machine/aquawatch.json` is rendered at deploy-time.
Placeholders replaced by the deploy script:

- `REAL_ACCOUNT_ID` → your current `$ACCOUNT_ID`
- `REAL_AWS_REGION` → your current `$AWS_REGION`

When `train=false`, a “UseExistingModel” step supplies a pre-existing model artifact for inference.
When `train=true`, the training job runs synchronously and the resulting model artifact is forwarded to infer.

## Development

- Code style: idiomatic Go, small helpers with explicit names
- Linting in-editor; compile with `go build ./...`
- Lambdas are plain Go binaries compiled for `linux/amd64` (or `arm64` if you change `ARCH`)

## Contributing

Contributions are welcome! To propose a change:

1. Fork the repo and create a feature branch
2. Make edits and include tests where possible
3. Run `go build ./...` to verify
4. Submit a pull request describing the change and motivation

Please keep commits focused and write clear PR descriptions. For larger refactors, open an issue first to discuss scope.

## Security

Do not commit secrets. Use AWS IAM roles and environment variables to configure credentials. If you discover a security issue, please open a private issue or contact the maintainers directly.

## License

This project is licensed under the MIT License. See [`LICENSE`](LICENSE) for details.

