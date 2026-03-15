#!/bin/bash
# Copy SSM params from staging to prod, overriding env-specific ones
# Usage: ./copy-staging-to-prod.sh
set -e

REGION="us-east-1"
SRC="/rail/staging"
DST="/rail/prod"

# Params to SKIP (will be set differently for prod or auto-set by Terraform)
SKIP=(
  "DATABASE_URL" "DATABASE_HOST" "DATABASE_PASSWORD" "DATABASE_NAME"
  "DATABASE_USER" "DATABASE_SSL_MODE" "REDIS_HOST" "REDIS_PORT"
  "BASE_URL" "database_url" "0"
)

echo "Fetching staging params..."
PARAMS=$(aws ssm get-parameters-by-path \
  --path "$SRC" \
  --with-decryption \
  --region "$REGION" \
  --query 'Parameters[].[Name,Value,Type]' \
  --output json)

echo "$PARAMS" | python3 -c "
import json, sys, subprocess

params = json.load(sys.stdin)
skip = set(['DATABASE_URL','DATABASE_HOST','DATABASE_PASSWORD','DATABASE_NAME',
            'DATABASE_USER','DATABASE_SSL_MODE','REDIS_HOST','REDIS_PORT',
            'BASE_URL','database_url','0'])

for name, value, ptype in params:
    key = name.split('/')[-1]
    if key in skip:
        print(f'SKIP: {name}')
        continue
    dst_name = name.replace('/rail/staging/', '/rail/prod/')
    cmd = ['aws', 'ssm', 'put-parameter',
           '--name', dst_name,
           '--value', value,
           '--type', ptype,
           '--region', '$REGION',
           '--overwrite']
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode == 0:
        print(f'OK: {dst_name}')
    else:
        print(f'ERR: {dst_name}: {result.stderr.strip()}')
"
