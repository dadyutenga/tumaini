#!/usr/bin/env bash
# Seeds a demo business with one user per role. Dev only. Needs the API running.
# Usage: API=http://localhost:8080 deploy/seed-dev.sh
# The staff user is linked to an employee record so My work, clock in and leave all work.
set -euo pipefail
API="${API:-http://localhost:8080}/api/v1"
case "$API" in
  http://localhost:*|http://127.0.0.1:*) ;;
  *) echo "Refusing to seed non-local API: $API" >&2; exit 1 ;;
esac
PASS="${SEED_PASSWORD:?Set SEED_PASSWORD (min 8 chars) to seed local dev accounts}"
j() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

reg=$(curl -sS -X POST "$API/setup" -H 'Content-Type: application/json' \
  -d "{\"business_name\":\"Demo Duka\",\"owner_name\":\"Demo Owner\",\"phone\":\"0700000001\",\"email\":\"owner@demo.test\",\"password\":\"$PASS\"}")
echo "$reg" | grep -q '"error"' && { echo "setup: $reg"; echo "(the business may already be set up; logging in)"; }
tok=$(curl -sS -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"owner@demo.test\",\"password\":\"$PASS\"}" | j "['data']['access_token']")
H=(-H "Authorization: Bearer $tok" -H 'Content-Type: application/json')
roles=$(curl -sS "${H[@]}" "$API/roles")
rid() { echo "$roles" | python3 -c "import sys,json;print(next(r['id'] for r in json.load(sys.stdin)['data'] if r['slug']=='$1'))"; }
bid=$(curl -sS "${H[@]}" "$API/branches" | python3 -c "import sys,json;print(json.load(sys.stdin)['data'][0]['id'])")
mk() { curl -sS -X POST "$API/users" "${H[@]}" -d "{\"name\":\"$1\",\"phone\":\"$2\",\"email\":\"$3\",\"password\":\"$PASS\",\"role_id\":\"$(rid "$4")\",\"branch_ids\":[\"$bid\"]}" | head -c 160; echo; }
mk "Demo Manager" 0700000002 manager@demo.test manager
mk "Demo Staff"   0700000003 staff@demo.test   staff
emp=$(curl -sS -X POST "$API/employees" "${H[@]}" -d '{"name":"Demo Staff","phone":"0700000003","pay_type":"monthly","base_pay":"500000"}' | j "['data']['id']" 2>/dev/null || true)
uid=$(curl -sS "${H[@]}" "$API/users" | python3 -c "import sys,json;print(next(u['id'] for u in json.load(sys.stdin)['data'] if u['email']=='staff@demo.test'))")
[ -n "${emp:-}" ] && curl -sS -X PUT "$API/users/$uid" "${H[@]}" -d "{\"employee_id\":\"$emp\"}" >/dev/null && echo "Linked staff@demo.test to employee $emp"
# Services: two catalog entries and a lead, so the services screens have something to show.
curl -sS -X POST "$API/service-catalog" "${H[@]}" -d '{"name":"Business registration","standard_price":"300000","government_fees":"50000","stages":[{"name":"Client intake","est_days":2},{"name":"Name registration","est_days":3},{"name":"TIN","est_days":5},{"name":"Licence","est_days":7}],"documents":[{"name":"ID copy","stage_position":1},{"name":"Proposed business names","stage_position":2}]}' >/dev/null
curl -sS -X POST "$API/service-catalog" "${H[@]}" -d '{"name":"Logo design","category":"design","standard_price":"80000","included_revisions":3}' >/dev/null
curl -sS -X POST "$API/clients" "${H[@]}" -d '{"name":"Mama Amina Shop","phone":"0711000111","source":"walk-in","needs":"Register a business and design a logo"}' >/dev/null
echo "Logins (password: $PASS): owner@demo.test, manager@demo.test, staff@demo.test"
