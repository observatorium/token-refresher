#!/bin/bash

# Runs a semi-realistic integration test with a producer generating metrics
# all being authenticated via Hydra and authorized with opa-ams.

set -euo pipefail

result=1
trap 'kill $(jobs -p); exit $result' EXIT

(DSN=memory hydra serve all --dangerous-force-http --disable-telemetry --config ./test/config/hydra.yaml) &

echo "-------------------------------------------"
echo "- Waiting for Hydra to come up...  -"
echo "-------------------------------------------"

until curl --output /dev/null --silent --fail --insecure http://127.0.0.1:4444/.well-known/openid-configuration; do
  printf '.'
  sleep 1
done

echo "-------------------------------------------"
echo "- Registering OIDC clients...         -"
echo "-------------------------------------------"

curl \
    --header "Content-Type: application/json" \
    --request POST \
    --data '{"audience": ["observatorium"], "client_id": "up", "client_secret": "secret", "grant_types": ["client_credentials"], "token_endpoint_auth_method": "client_secret_basic"}' \
    http://127.0.0.1:4445/clients

(
  ${OBSERVATORIUM} \
    --web.listen=0.0.0.0:8443 \
    --web.internal.listen=0.0.0.0:8448 \
    --web.healthchecks.url=http://127.0.0.1:8443 \
    --metrics.read.endpoint=http://127.0.0.1:9091 \
    --metrics.write.endpoint=http://127.0.0.1:19291 \
    --rbac.config=./test/config/rbac.yaml \
    --tenants.config=./test/config/tenants.yaml \
    --log.level=debug
) &

(
  ${THANOS} receive \
    --receive.hashrings-file=./test/config/hashrings.json \
    --receive.local-endpoint=127.0.0.1:10901 \
    --receive.default-tenant-id="1610b0c3-c509-4592-a256-a1871353dbfa" \
    --grpc-address=127.0.0.1:10901 \
    --http-address=127.0.0.1:10902 \
    --remote-write.address=127.0.0.1:19291 \
    --label=receive_replica=\"0\" \
    --log.level=error \
    --tsdb.path="$(mktemp -d)"
) &

(
  ${THANOS} query \
    --grpc-address=127.0.0.1:10911 \
    --http-address=127.0.0.1:9091 \
    --endpoint=127.0.0.1:10901 \
    --log.level=error \
    --query.replica-label=receive_replica
) &

(
  ./token-refresher \
      --oidc.issuer-url=http://localhost:4444/ \
      --oidc.client-id=up \
      --oidc.client-secret=secret \
      --oidc.audience=observatorium \
      --url=http://127.0.0.1:8443 \
      --file=./tmp/token
) &

echo "-------------------------------------------"
echo "- Waiting for dependencies to come up...  -"
echo "-------------------------------------------"
sleep 10

until curl --output /dev/null --silent --fail http://127.0.0.1:8081/ready; do
  printf '.'
  sleep 1
done

echo "-------------------------------------------"
echo "- Bootstrap metrics                       -"
echo "-------------------------------------------"

# Before the query can select the receiver, the latter needs to ingest at least
# 1 sample for each tenant.
if ${UP} \
  --listen=0.0.0.0:8888 \
  --endpoint-type=metrics \
  --endpoint-write=http://127.0.0.1:19291/api/v1/receive \
  --log.level=error \
  --duration=1s \
  --latency=1s \
  --period=500ms ; then
  result=0
else
  result=1
  echo "-------------------------------------------"
  echo "- tests: FAILED                           -"
  echo "-------------------------------------------"
fi

# Ensure that the query has updated the minTime value of the receiver.
until [ "$(curl --silent --fail http://127.0.0.1:9091/api/v1/stores | jq ".data.receive[0].minTime < now * 1000")" == "true" ]; do
  printf '.'
  sleep 1
done

echo "-------------------------------------------"
echo "- tests: OK                               -"
echo "-------------------------------------------"

echo "-------------------------------------------"
echo "- Token File                              -"
echo "-------------------------------------------"

if ${UP} \
  --listen=0.0.0.0:8888 \
  --endpoint-type=metrics \
  --endpoint-read=http://127.0.0.1:8443/api/metrics/v1/test-oidc \
  --endpoint-write=http://127.0.0.1:8443/api/metrics/v1/test-oidc/api/v1/receive \
  --period=500ms \
  --initial-query-delay=250ms \
  --threshold=1 \
  --latency=10s \
  --duration=10s \
  --log.level=error \
  --name=observatorium_write \
  --labels='_id="test"' \
  --token-file=./tmp/token; then
  result=0
  echo "-------------------------------------------"
  echo "- Token File: OK                          -"
  echo "-------------------------------------------"
else
  result=1
  echo "-------------------------------------------"
  echo "- Token File: FAILED                      -"
  echo "-------------------------------------------"
  exit 1
fi

echo "-------------------------------------------"
echo "- Token Proxy                             -"
echo "-------------------------------------------"

if ${UP} \
  --listen=0.0.0.0:8888 \
  --endpoint-type=metrics \
  --endpoint-read=http://127.0.0.1:8080/api/metrics/v1/test-oidc \
  --endpoint-write=http://127.0.0.1:8080/api/metrics/v1/test-oidc/api/v1/receive \
  --period=500ms \
  --initial-query-delay=250ms \
  --threshold=1 \
  --latency=10s \
  --duration=10s \
  --log.level=error \
  --name=observatorium_write \
  --labels='_id="test"'; then
  result=0
  echo "-------------------------------------------"
  echo "- Token Proxy: OK                         -"
  echo "-------------------------------------------"
else
  result=1
  echo "-------------------------------------------"
  echo "- Token Proxy: FAILED                     -"
  echo "-------------------------------------------"
  exit 1
fi

echo "-------------------------------------------"
echo "- All tests: OK                           -"
echo "-------------------------------------------"
exit 0
