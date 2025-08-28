# AquaWatch — AI‑powered water intelligence (Backend)

AquaWatch is a small, end-to-end pipeline that:

- Fetches real-time USGS water data for a station/parameter
- Preprocesses the data into a numeric CSV dataset and enriches it with NOAA weather context
- Optionally trains an XGBoost model via AWS Step Functions (synchronous)
- Invokes a SageMaker endpoint to generate predictions
- Exposes a simple HTTP API to trigger the pipeline

The project is written in Go and designed to run on AWS (Lambda, S3, Step Functions, SageMaker).

## DynamoDB

The deploy script ensures the following DynamoDB tables exist (all are PAY_PER_REQUEST):

- Prediction Tracker
  - Table: `prediction-tracker` (override via `PREDICTION_TRACKER_TABLE`)
  - Keys: PK `site` (String), SK `status` (String)
  - Attributes: `createdon` (Number, epoch ms), `updatedon` (Number, epoch ms)

- Alert Tracker
  - Table: `alert-tracker` (override via `ALERT_TRACKER_TABLE`)
  - Keys: PK `createdon` (Number, epoch ms)
  - GSI: `gsi_recent` with PK `gsi_pk` (String, constant "recent" for new records) and SK `createdon` (Number)

- Train Model Tracker
  - Table: `train-model-tracker` (override via `TRAIN_MODEL_TRACKER_TABLE`)
  - Keys: PK `uuid` (String), SK `createdon` (Number, epoch ms)
  - GSI: `gsi_recent` with PK `gsi_pk` (String, constant "recent" for new records) and SK `createdon` (Number)

The script creates/updates the tables and waits until active:

```bash
./scripts/install.sh
```

## Alerts (SNS)

The deploy script also ensures an SNS topic exists for email alerts/notifications:

- Topic name: `SNS_TOPIC_NAME` env var (default `aquawatch-alerts`)
- The topic is created if missing; the script prints the Topic ARN

Subscribe emails via the API (requires email confirmation):

```bash
curl -X POST "http://localhost:8080/alerts/subscribe" \
  -H "Content-Type: application/json" \
  -d '{"email":"you@example.com"}'
```

Notes:
- The API validates the email using a regex and returns 400 if invalid.
- For email protocol, SNS returns SubscriptionArn as "pending confirmation" until the user confirms via the link in the email.

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
# Optional: override alerts SNS topic name (created if missing)
export SNS_TOPIC_NAME=aquawatch-alerts
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

## Initial AWS Setup (one-time)

1) Decide region
- Choose an AWS region that supports SageMaker, Step Functions, and Lambda (e.g., `us-west-2`).
- Export env vars:
```bash
export AWS_REGION=us-west-2
export ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
```

2) Create S3 bucket
- Create a bucket to store processed CSV and model artifacts:
```bash
aws s3 mb s3://<your-aquawatch-bucket> --region $AWS_REGION
export S3_BUCKET=<your-aquawatch-bucket>
```

3) SageMaker setup (domain, model, endpoint config, endpoint)
- Create a SageMaker Domain (Studio) once per account/region if not already present (can be done via Console: SageMaker → Studio → Set up domain). This is optional for inference-only, but recommended for manageability.
- Build or select a container image (XGBoost example used here) and produce a model artifact in S3.
- Create a SageMaker Model referencing the container and model artifact:
```bash
aws sagemaker create-model \
  --model-name aquawatch-xgb-model \
  --primary-container Image=246618743249.dkr.ecr.$AWS_REGION.amazonaws.com/sagemaker-xgboost:1.7-1,ModelDataUrl=s3://$S3_BUCKET/model/aquawatch-train-default/output/model.tar.gz \
  --execution-role-arn arn:aws:iam::$ACCOUNT_ID:role/aquawatch-sagemaker-exec-role
```
- Create an endpoint configuration:
```bash
aws sagemaker create-endpoint-config \
  --endpoint-config-name aquawatch-xgb-config \
  --production-variants '[{"VariantName":"AllTraffic","ModelName":"aquawatch-xgb-model","InitialInstanceCount":1,"InstanceType":"ml.c5.large"}]'
```
- Create an endpoint:
```bash
aws sagemaker create-endpoint \
  --endpoint-name aquawatch-xgb \
  --endpoint-config-name aquawatch-xgb-config
```
- Wait until endpoint is `InService`, then set:
```bash
export SAGEMAKER_ENDPOINT=aquawatch-xgb
```

4) IAM roles
- Lambda execution role (created automatically by `scripts/install.sh` if missing): `aquawatch-lambda-role` with S3 + SageMaker Invoke permissions.
- Step Functions execution role: ensure `SFN_ROLE_ARN` in `scripts/install.sh` points to a role that allows invoking the Lambda functions and SageMaker training jobs (if `train=true`).

5) Example data URLs
- USGS water data (instantaneous values, one site):
  - `https://waterservices.usgs.gov/nwis/iv/?format=json&sites=03339000&parameterCd=00060&period=P1D`
- NOAA Temperature via NWS API (derived via `internal/weather.go`):
  - Get forecast URL: `https://api.weather.gov/points/<lat>,<lon>`
  - Then fetch the `forecast` URL returned in the response.

6) Default model in S3
- Place a default model tarball at:
  - `s3://$S3_BUCKET/model/aquawatch-train-default/output/model.tar.gz`
- Set the environment variable so infer can locate it when `train=false`:
```bash
export DEFAULT_MODEL=s3://$S3_BUCKET/model/aquawatch-train-default/output/model.tar.gz
```

Trigger ingestion:

```bash
curl "http://localhost:8080/ingest?station=03339000&parameter=00060&train=false"
```

Check prediction status (in-progress if created within last 5 minutes):

```bash
curl "http://localhost:8080/prediction/status?site=03339000"
# Optional: override status key (defaults to started)
curl "http://localhost:8080/prediction/status?site=03339000&status=started"
```

Subscribe to alerts via API (email must confirm):

```bash
curl -X POST "http://localhost:8080/alerts/subscribe" \
  -H "Content-Type: application/json" \
  -d '{"email":"you@example.com"}'
```

Generate anomaly report PDF (image + items), upload to S3, and get a presigned URL (5 min):

```bash
curl -X POST "http://localhost:8080/report/pdf" \
  -H "Content-Type: application/json" \
  -d '{
    "image_base64": "<base64 image>",
    "items": [
      {"site":"03339000","reason":">10% deviation","predicted_value":66.2,"anomaly_date":"2025-09-01"}
    ]
  }'
```

List recent alerts from the Alert Tracker table (last 10 minutes by default):

```bash
curl "http://localhost:8080/alerts?minutes=10"
```

Example response:

```json
{
  "site": "03339000",
  "status": "started",
  "in_progress": true,
  "createdon_ms": 1732470000000,
  "updatedon_ms": 1732470000000
}
```

## API Endpoints

- Ingest pipeline (supports multiple stations)
  - GET `/ingest?stations=03339000,03339001&parameter=00060&train=false`
  - Or repeat `station` multiple times: `/ingest?station=03339000&station=03339001`

- Prediction status
  - GET `/prediction/status?site=03339000&status=started`

- Alerts
  - POST `/alerts/subscribe` body: `{ "email": "you@example.com" }`
  - GET `/alerts?minutes=10`

- Anomaly check
  - POST `/anomaly/check`
  - Body:
    ```json
    {
      "sites": ["03339000", "03339001"],
      "min_lat": 0, "min_lng": 0, "max_lat": 0, "max_lng": 0,
      "threshold_percent": 10,
      "parameter": "00060"
    }
    ```

- PDF report
  - POST `/report/pdf` body: `{ "image_base64": "...", "items": [{"site":"...","reason":"...","predicted_value": 1.2, "anomaly_date": "2025-01-01"}] }`

- Train model tracker (descending by createdon)
  - GET `/train/models?minutes=60`
  - Response shape:
    ```json
    { "items": [ { "uuid": "aquawatch-train-123", "createdon": 1732470000000, "sites": ["03339000"] } ] }
    ```

## Authentication and CORS

- CORS: Responses include permissive headers allowing any origin.
- Authentication: Vonage Verify-based OTP can be enabled via `VONAGE_VERIFY_ENABLED` (set to `false` to disable).
  - Start: POST `/sms/send` body `{ "phone_e164": "+15551234567", "brand": "AquaWatch" }` → `{ "session_id": "..." }`
  - Verify: POST `/sms/verify` body `{ "session_id": "...", "code": "123456", "phone_e164": "+15551234567" }` → `{ "token": "..." }`
  - Subsequent requests can pass `X-Session-Token: <token>` header instead of Vonage headers.

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

