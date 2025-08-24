#!/usr/bin/env bash
# AquaWatch deploy script
# - Builds and deploys Lambda functions (preprocess, infer)
# - Creates/updates the Step Functions state machine from infra/state_machine/aquawatch.json
# - Substitutes REAL_ACCOUNT_ID and REAL_AWS_REGION in the definition
# Requirements: awscli v2, permissions for IAM/Lambda/StepFunctions
set -euo pipefail

# -------------------- Config & Inputs --------------------

# AWS account ID (required)
ACCOUNT_ID="${ACCOUNT_ID}"
if [ -z "$ACCOUNT_ID" ]; then
  echo "ACCOUNT_ID is not set"
  exit 1
fi

# AWS region (defaults to us-west-2)
AWS_REGION="${AWS_REGION:-us-west-2}"

# Lambda execution role (created if missing)
LAMBDA_ROLE_NAME="${LAMBDA_ROLE_NAME:-aquawatch-lambda-role}"

# S3 bucket used by preprocess/infer (required)
S3_BUCKET="${S3_BUCKET}"
if [ -z "$S3_BUCKET" ]; then
  echo "S3_BUCKET is not set"
  exit 1
fi

# SageMaker endpoint used by infer (required)
SAGEMAKER_ENDPOINT="${SAGEMAKER_ENDPOINT}"
if [ -z "$SAGEMAKER_ENDPOINT" ]; then
  echo "SAGEMAKER_ENDPOINT is not set"
  exit 1
fi

# Step Functions names/roles
STATE_MACHINE_NAME="${STATE_MACHINE_NAME:-aquawatch-pipeline}"
SFN_ROLE_ARN="${SFN_ROLE_ARN:-arn:aws:iam::${ACCOUNT_ID}:role/service-role/StepFunctions-aquawatch-role-2sur8cc9m}"

# Architecture: arm64 or x86_64
ARCH="${ARCH:-amd64}"
GOARCH="amd64"; [[ "$ARCH" == "arm64" ]] && GOARCH="arm64"

# Function names
PREPROCESS_FN="${PREPROCESS_FN:-aquawatch-preprocess}"
INFER_FN="${INFER_FN:-aquawatch-infer}"

# -------------------- Bootstrap --------------------

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

echo "Region: $AWS_REGION, Account: $ACCOUNT_ID, Arch: $ARCH ($GOARCH)"
echo "Using S3 bucket: $S3_BUCKET"
echo "SageMaker endpoint=$SAGEMAKER_ENDPOINT"

aws configure set default.region "$AWS_REGION"

# -------------------- IAM Helpers --------------------

role_arn() {
  aws iam get-role --role-name "$LAMBDA_ROLE_NAME" --query 'Role.Arn' --output text 2>/dev/null || true
}

create_or_get_role() {
  local arn
  arn="$(role_arn)"
  if [[ -z "$arn" ]]; then
    echo "Creating IAM role $LAMBDA_ROLE_NAME ..."
    aws iam create-role \
      --role-name "$LAMBDA_ROLE_NAME" \
      --assume-role-policy-document '{
        "Version":"2012-10-17",
        "Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]
      }' >/dev/null

    aws iam attach-role-policy \
      --role-name "$LAMBDA_ROLE_NAME" \
      --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

    # Inline policy with S3 + InvokeEndpoint
    aws iam put-role-policy \
      --role-name "$LAMBDA_ROLE_NAME" \
      --policy-name aquawatch-inline \
      --policy-document "{
        \"Version\": \"2012-10-17\",
        \"Statement\": [
          {
            \"Effect\": \"Allow\",
            \"Action\": [\"s3:GetObject\",\"s3:PutObject\",\"s3:ListBucket\"],
            \"Resource\": [
              \"arn:aws:s3:::${S3_BUCKET}\",
              \"arn:aws:s3:::${S3_BUCKET}/*\"
            ]
          },
          {
            \"Effect\": \"Allow\",
            \"Action\": [\"sagemaker:InvokeEndpoint\"],
            \"Resource\": \"*\"
          }
        ]
      }" >/dev/null

    echo "Waiting for role to propagate..."
    sleep 8
    arn="$(role_arn)"
  fi
  echo "$arn"
}

# -------------------- Build & Deploy Lambdas --------------------

build_zip() {
  local lambda_dir="$1" out_dir="$2"
  mkdir -p "$out_dir"
  pushd "$lambda_dir" >/dev/null
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -o bootstrap .
  zip -q "$out_dir/package.zip" bootstrap
  rm -f bootstrap
  popd >/dev/null
}

upsert_lambda() {
  local fn_name="$1" zip_path="$2" role_arn="$3"
  if aws lambda get-function --function-name "$fn_name" >/dev/null 2>&1; then
    echo "Updating code for $fn_name ..."
    aws lambda update-function-code \
      --function-name "$fn_name" \
      --zip-file "fileb://$zip_path" >/dev/null
    sleep 10
    echo "Updating config for $fn_name ..."
    aws lambda update-function-configuration \
      --function-name "$fn_name" \
      --runtime provided.al2 \
      --handler bootstrap \
      --role "$role_arn" \
      --timeout 60 \
      --memory-size 512 >/dev/null
  else
    echo "Creating $fn_name ..."
    aws lambda create-function \
      --function-name "$fn_name" \
      --runtime provided.al2 \
      --handler bootstrap \
      --role "$role_arn" \
      --timeout 60 \
      --memory-size 512 \
      --zip-file "fileb://$zip_path" \
      --publish >/dev/null
  fi
}

set_env() {
  local fn_name="$1"; shift
  aws lambda update-function-configuration \
    --function-name "$fn_name" \
    --environment "Variables={$*}" >/dev/null
}

# -------------------- Step Functions --------------------

upsert_state_machine() {
  local rendered="/tmp/aquawatch_state_machine.json"
  # Replace placeholders for account id and region within the definition
  sed -e "s#REAL_ACCOUNT_ID#${ACCOUNT_ID}#g" \
      -e "s#REAL_AWS_REGION#${AWS_REGION}#g" \
      "$REPO_ROOT/infra/state_machine/aquawatch.json" > "$rendered"

  local existing
  existing=$(aws stepfunctions list-state-machines --query "stateMachines[?name=='$STATE_MACHINE_NAME'].stateMachineArn | [0]" --output text)
  if [[ -z "$existing" || "$existing" == "None" ]]; then
    echo "Creating Step Functions state machine $STATE_MACHINE_NAME ..."
    aws stepfunctions create-state-machine \
      --name "$STATE_MACHINE_NAME" \
      --definition "file://$rendered" \
      --role-arn "$SFN_ROLE_ARN" >/dev/null
  else
    echo "Updating Step Functions state machine $STATE_MACHINE_NAME ..."
    aws stepfunctions update-state-machine \
      --state-machine-arn "$existing" \
      --definition "file://$rendered" >/dev/null
  fi
}

# -------------------- Main --------------------

main() {
  local ROLE_ARN
  ROLE_ARN="$(create_or_get_role)"
  echo "Using role: $ROLE_ARN"

  local BUILD_ROOT
  BUILD_ROOT="/tmp/aquawatch"
  rm -rf "$BUILD_ROOT"

  # Build lambdas (preprocess and infer)
  build_zip "lambdas/preprocess" "$BUILD_ROOT/preprocess"
  build_zip "lambdas/infer" "$BUILD_ROOT/infer"

  # Upsert functions
  upsert_lambda "$PREPROCESS_FN" "$BUILD_ROOT/preprocess/package.zip" "$ROLE_ARN"
  upsert_lambda "$INFER_FN"      "$BUILD_ROOT/infer/package.zip"      "$ROLE_ARN"

  # Environment variables
  sleep 10
  set_env "$INFER_FN" "SAGEMAKER_ENDPOINT=$SAGEMAKER_ENDPOINT,S3_BUCKET=$S3_BUCKET"

  # Create or update Step Functions state machine
  upsert_state_machine

  echo "Deployment complete. Functions: $PREPROCESS_FN, $INFER_FN. State Machine: $STATE_MACHINE_NAME"
}

main "$@"
