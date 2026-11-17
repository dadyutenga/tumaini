#!/usr/bin/env bash
# Seeds a demo business with one user per role. Dev only. Needs the API running.
# Usage: API=http://localhost:8080 deploy/seed-dev.sh
set -euo pipefail
API="${API:-http://localhost:8080}/api/v1"
PASS="${SEED_PASSWORD:-Tumaini#2026}"
j() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

reg=$(curl -sS -X POST "$API/auth/register" -H 'Content-Type: application/json' \
  -d "{\"business_name\":\"Demo Duka\",\"owner_name\":\"Demo Owner\",\"phone\":\"0700000001\",\"email\":\"owner@demo.test\",\"password\":\"$PASS\"}")
echo "$reg" | grep -q '"error"' && { echo "register: $reg"; echo "(owner may already exist; logging in)"; }
tok=$(curl -sS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"owner@demo.test\",\"password\":\"$PASS\"}" | j "['data']['access_token']")
H=(-H "Authorization: Bearer $tok" -H 'Content-Type: application/json')
roles=$(curl -sS "${H[@]}" "$API/roles")
rid() { echo "$roles" | python3 -c "import sys,json;print(next(r['id'] for r in json.load(sys.stdin)['data'] if r['slug']=='$1'))"; }
bid=$(curl -sS "${H[@]}" "$API/branches" | python3 -c "import sys,json;print(json.load(sys.stdin)['data'][0]['id'])")
mk() { curl -sS -X POST "$API/users" "${H[@]}" -d "{\"name\":\"$1\",\"phone\":\"$2\",\"email\":\"$3\",\"password\":\"$PASS\",\"role_id\":\"$(rid "$4")\",\"branch_ids\":[\"$bid\"]}" | head -c 160; echo; }
mk "Demo Manager" 0700000002 manager@demo.test manager
mk "Demo Staff"   0700000003 staff@demo.test   staff
echo "Logins (password: $PASS): owner@demo.test, manager@demo.test, staff@demo.test"
