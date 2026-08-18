#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

HOST='cult-memory-32318.j77.aws-ap-south-1.cockroachlabs.cloud'
PORT='26257'
USER_NAME='danielkock73_gmail_c'
DB_NAME='defaultdb'
CONTAINER='cult-memory-demo'
BASE='http://127.0.0.1:8080'
USER_ID='cult-demo-user'

if ! docker image inspect cult-memory >/dev/null 2>&1; then
  echo 'Building Cult Memory image...'
  docker build -t cult-memory .
fi

read -rsp 'CockroachDB password: ' PASSWORD
echo
ENCODED_PASSWORD="$(P="$PASSWORD" python3 -c 'import os, urllib.parse; print(urllib.parse.quote(os.environ["P"], safe=""))')"
unset PASSWORD
COCKROACH_URL="postgresql://${USER_NAME}:${ENCODED_PASSWORD}@${HOST}:${PORT}/${DB_NAME}?sslmode=require"
unset ENCODED_PASSWORD

docker rm -f "$CONTAINER" >/dev/null 2>&1 || true

echo 'Starting Cult Memory locally (no forwarded port needed)...'
docker run -d --name "$CONTAINER" -p 127.0.0.1:8080:8080 \
  -e COCKROACH_URL="$COCKROACH_URL" cult-memory >/dev/null
unset COCKROACH_URL

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

ready=''
for _ in $(seq 1 30); do
  if curl -fsS "$BASE/api/health" >/tmp/cult-memory-health.json 2>/dev/null; then
    ready=1
    break
  fi
  sleep 1
done

if [[ -z "$ready" ]]; then
  echo 'Cult Memory did not become healthy. Recent logs:'
  docker logs --tail 40 "$CONTAINER" 2>&1 || true
  exit 1
fi

echo '✅ App + Cult gate + CockroachDB are healthy'

FACT='Remember that Cult is written in Go and deployments require human approval.'
SESSION_ONE="demo-$(date +%s)-1"
SESSION_TWO="demo-$(date +%s)-2"

CHAT_ONE="$(python3 - <<PY
import json
print(json.dumps({'user_id':'$USER_ID','session_id':'$SESSION_ONE','message':'$FACT'}))
PY
)"

RESPONSE_ONE="$(curl -fsS -X POST "$BASE/api/chat" \
  -H 'Content-Type: application/json' \
  --data "$CHAT_ONE")"

PROPOSAL_ID="$(RESP="$RESPONSE_ONE" python3 - <<'PY'
import json, os
r=json.loads(os.environ['RESP'])
p=r.get('proposal') or {}
print(p.get('id',''))
PY
)"

if [[ -z "$PROPOSAL_ID" ]]; then
  echo '❌ Fact did not produce a memory proposal.'
  RESP="$RESPONSE_ONE" python3 - <<'PY'
import json, os
print(json.dumps(json.loads(os.environ['RESP']), indent=2))
PY
  exit 1
fi

echo '✅ Fact -> memory proposal created (still unapproved)'

APPROVAL_BODY="$(python3 - <<PY
import json
print(json.dumps({'user_id':'$USER_ID'}))
PY
)"

APPROVED="$(curl -fsS -X POST "$BASE/api/memories/$PROPOSAL_ID/approve" \
  -H 'Content-Type: application/json' \
  --data "$APPROVAL_BODY")"

IS_APPROVED="$(RESP="$APPROVED" python3 - <<'PY'
import json, os
print(str(bool(json.loads(os.environ['RESP']).get('approved'))).lower())
PY
)"

if [[ "$IS_APPROVED" != 'true' ]]; then
  echo '❌ Proposal approval failed.'
  exit 1
fi

echo '✅ Human approval -> memory activated'

QUESTION='What do you remember about Cult and deployments?'
CHAT_TWO="$(python3 - <<PY
import json
print(json.dumps({'user_id':'$USER_ID','session_id':'$SESSION_TWO','message':'$QUESTION'}))
PY
)"

RESPONSE_TWO="$(curl -fsS -X POST "$BASE/api/chat" \
  -H 'Content-Type: application/json' \
  --data "$CHAT_TWO")"

RESP="$RESPONSE_TWO" python3 - <<'PY'
import json, os, sys
r=json.loads(os.environ['RESP'])
recalled=r.get('recalled') or []
answer=r.get('answer','')
print(f'✅ New session -> recalled approved memories: {len(recalled)}')
print('Agent:', answer)
if not recalled:
    print('❌ No approved memory was recalled.')
    sys.exit(1)
print('\n🏁 PASS: fact -> proposal -> approve -> new session -> recall')
PY
