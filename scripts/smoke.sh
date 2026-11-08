#!/usr/bin/env bash
# End-to-end smoke test against a running `docker compose up` (PLAN step 12).
#
# Walks the UI's flow over HTTP through nginx: create a campaign from a
# brief, stream the generated message (SSE), save it, approve both
# channels, and wait for the worker to finish. Then checks that
#   - SSE events arrive one by one through nginx (not buffered to the end),
#   - every snapshot row ended up sent/failed/skipped,
#   - the mock provider delivered exactly as many messages as the DB says
#     were sent (no duplicates, nothing lost),
#   - no one-second window exceeded the provider limit,
#   - the logs contain no e-mail addresses or phone numbers.
# The test campaign is deleted afterwards (SMOKE_KEEP=1 keeps it).
#
# Refuses to run unless the API uses the mock LLM: no paid calls.
# Needs curl and jq. Assumes no other campaign is sending at the same time.

set -euo pipefail

WEB=${SMOKE_WEB_URL:-http://localhost:3000}
PROVIDER=${SMOKE_PROVIDER_URL:-http://localhost:8090}
COMPOSE=${SMOKE_COMPOSE:-docker compose}
BRIEF="İstanbul'da yaşayan, son 30 günde sepete ürün ekleyip alışveriş yapmamış kullanıcılara %10 indirim mesajı gönder."

step() { printf '\n== %s\n' "$*"; }
ok() { printf '   ok: %s\n' "$*"; }
fail() { printf '   FAIL: %s\n' "$*" >&2; exit 1; }

for tool in curl jq; do
	command -v "$tool" >/dev/null || fail "$tool is required"
done

tmp=$(mktemp -d)
campaign_id=""
cleanup() {
	if [[ -n $campaign_id && ${SMOKE_KEEP:-0} != 1 ]]; then
		# deliveries go with the campaign (ON DELETE CASCADE).
		$COMPOSE exec -T postgres psql -q -U scc -d scc \
			-c "DELETE FROM campaigns WHERE id = $campaign_id" >/dev/null 2>&1 &&
			echo "   test campaign $campaign_id deleted" ||
			echo "   could not delete test campaign $campaign_id" >&2
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT

# api METHOD PATH [JSON] -> prints the body, fails on a non-2xx status.
api() {
	local code
	code=$(curl -sS -o "$tmp/body" -w '%{http_code}' -X "$1" \
		-H 'Content-Type: application/json' ${3:+--data "$3"} "$WEB$2") ||
		fail "$1 $2: request failed"
	[[ $code == 2* ]] || fail "$1 $2: HTTP $code: $(cat "$tmp/body")"
	cat "$tmp/body"
}

step "Waiting for the UI and API at $WEB"
for _ in $(seq 60); do
	curl -sf "$WEB/api/info" >/dev/null 2>&1 && break
	sleep 1
done
provider=$(api GET /api/info | jq -r .llm_provider)
[[ $provider == mock ]] ||
	fail "API uses LLM provider '$provider'; the smoke test runs only with LLM_PROVIDER=mock (no paid calls)"
ok "API is up, LLM provider: mock"

step "Static UI"
curl -sf "$WEB/" | grep -q '<div id="app">' || fail "index.html not served"
curl -sf "$WEB/campaigns/1" | grep -q '<div id="app">' || fail "client-side route not served by index.html"
ok "index.html served at / and at /campaigns/1"

step "Create campaign from a brief"
created=$(api POST /api/campaigns "$(jq -n --arg b "$BRIEF" '{brief: $b}')")
campaign_id=$(jq -r .campaign.id <<<"$created")
[[ $campaign_id =~ ^[0-9]+$ ]] || { campaign_id=""; fail "no campaign id: $created"; }
rule_status=$(jq -r .campaign.rule.status <<<"$created")
reachable=$(jq -r .campaign.audience.reachable <<<"$created")
[[ $rule_status == ok ]] || fail "rule status $rule_status, want ok"
((reachable > 0)) || fail "empty audience"
ok "campaign $campaign_id: rule ok, $reachable reachable users"

step "Generate content (SSE through nginx)"
events_url=$(api POST "/api/campaigns/$campaign_id/content/generations" | jq -r .events_url)
# Stamp every line with its arrival time. A buffering proxy (or gzip, which
# browsers ask for with Accept-Encoding; --compressed does the same) would deliver
# all events in one burst at the end.
curl -sSN --compressed --max-time 60 "$WEB$events_url" |
	while IFS= read -r line; do printf '%s\t%s\n' "$(date +%s%N)" "$line"; done >"$tmp/sse"
grep -q $'\tevent: validated$' "$tmp/sse" || fail "no validated event: $(cut -f2 "$tmp/sse" | grep '^event:' | sort | uniq -c)"
deltas=$(grep -c $'\tevent: .*\\.delta$' "$tmp/sse" || true)
first=$(grep -m1 $'\tevent: .*\\.delta$' "$tmp/sse" | cut -f1)
last=$(grep -m1 $'\tevent: validated$' "$tmp/sse" | cut -f1)
spread_ms=$(((last - first) / 1000000))
((deltas > 10)) || fail "only $deltas delta events"
((spread_ms > 300)) || fail "deltas arrived within ${spread_ms} ms: the stream looks buffered"
ok "$deltas delta events spread over ${spread_ms} ms, then validated"

# The validated event's data line follows its event line.
message=$(grep -A1 $'\tevent: validated$' "$tmp/sse" | tail -1 | cut -f2 | sed 's/^data: //' | jq -c .message)
saved=$(api PUT "/api/campaigns/$campaign_id/content" "$message")
[[ $(jq -r .campaign.content_validation.valid <<<"$saved") == true ]] || fail "saved content is not valid"
[[ $(jq -r .campaign.approvable <<<"$saved") == true ]] || fail "campaign is not approvable"
ok "message saved and valid"

step "Approve SMS + email"
before=$(curl -sf "$PROVIDER/stats") || fail "mock provider stats not reachable at $PROVIDER"
approved=$(api POST "/api/campaigns/$campaign_id/approve" '{"channels":{"sms":true,"email":true},"partial_ack":false}')
snap_sms=$(jq -r .snapshot.sms <<<"$approved")
snap_email=$(jq -r .snapshot.email <<<"$approved")
ok "snapshot: $snap_sms SMS, $snap_email email"

step "Wait for sending to finish (dashboard SSE)"
# The dashboard stream sends the full state every second and closes after
# the event with done: true.
curl -sSN --compressed --max-time 180 "$WEB/api/campaigns/$campaign_id/events" | grep '^data: ' >"$tmp/progress" || true
final=$(tail -1 "$tmp/progress" | sed 's/^data: //')
[[ $(jq -r .done <<<"$final") == true ]] || fail "campaign did not complete within 180 s: $final"
ok "completed after $(wc -l <"$tmp/progress") progress events"

step "Check the outcome"
after=$(curl -sf "$PROVIDER/stats")
for ch in sms email; do
	read -r total pending sending sent failed skipped < <(jq -r --arg c "$ch" \
		'.channels[$c] | "\(.total) \(.pending) \(.sending) \(.sent) \(.failed) \(.skipped)"' <<<"$final")
	delivered=$(($(jq -r --arg c "$ch" '.[$c].delivered' <<<"$after") - $(jq -r --arg c "$ch" '.[$c].delivered' <<<"$before")))
	max_ps=$(jq -r --arg c "$ch" '.[$c].max_per_second' <<<"$after")
	((pending == 0 && sending == 0)) || fail "$ch: $pending pending, $sending sending"
	((sent + failed + skipped == total)) || fail "$ch: $sent+$failed+$skipped != $total"
	((delivered == sent)) || fail "$ch: mock delivered $delivered, DB sent $sent"
	((max_ps <= 100)) || fail "$ch: $max_ps requests in one second"
	ok "$ch: $sent sent, $failed failed, $skipped skipped of $total; mock delivered $delivered; max $max_ps/s"
done
[[ $(jq -r .snapshot.sms <<<"$approved") == "$(jq -r .channels.sms.total <<<"$final")" ]] || fail "SMS total differs from snapshot"
[[ $(jq -r .snapshot.email <<<"$approved") == "$(jq -r .channels.email.total <<<"$final")" ]] || fail "email total differs from snapshot"
ok "totals match the approved snapshot"

step "Logs contain no personal data"
if $COMPOSE logs --no-color api worker mockprovider >"$tmp/logs" 2>/dev/null; then
	# Seed data: user00001@example.test, +905550000001.
	if grep -Eq '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}|\+90[0-9]{10}' "$tmp/logs"; then
		fail "e-mail address or phone number found in the logs"
	fi
	ok "$(wc -l <"$tmp/logs") log lines checked"
else
	echo "   skipped: '$COMPOSE logs' not available"
fi

printf '\nSmoke test passed.\n'
