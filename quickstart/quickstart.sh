#!/usr/bin/env bash
# From a server build to a running game server in one go: makes the app, uploads the build, makes a version from
# a settings file, waits until a region has it, starts a game server and prints its address.
#
#   export API=https://studio.survivalservers.com API_KEY=gsk_live_...
#   ./quickstart.sh linux   registry.example.com/you/your-server:1.0.0  version-linux.json
#   ./quickstart.sh windows YourServer.zip                 version-windows.json
#
# The settings file is a version without its build (the samples have one for each system); "build" is filled in.
# API is the API's address (pass it: the default is a stand-in until the public address is chosen).
# Optional: APP (default plain-server), CONTINENT (default europe), TIER (default high-frequency on Linux, standard on
# Windows: what the open region sells).
# PRACTICE=1 runs against the practice server, which answers with examples: no file upload, no waiting.
# Needs bash, curl, jq and sha256sum (or shasum).
set -euo pipefail

# A test names its own address (GS_API_URL_REQUIRED=1 refuses the placeholder default).
if [ -z "${API:-}" ] && [ "${GS_API_URL_REQUIRED:-}" = 1 ]; then echo "no API address given: tests must set one (API)" >&2; exit 2; fi
API="${API:-https://studio.survivalservers.com}"
APP="${APP:-plain-server}"
CONTINENT="${CONTINENT:-europe}"
TIER="${TIER:-}"
PRACTICE="${PRACTICE:-0}"
POLL_SECONDS="${POLL_SECONDS:-5}"

usage() { sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
[ $# -eq 3 ] || usage
os="$1" source="$2" settings="$3"
[ "$os" = linux ] || [ "$os" = windows ] || usage
# A Linux app made standard cannot start in the open region, which sells Linux on high-frequency only.
[ -n "$TIER" ] || { [ "$os" = linux ] && TIER=high-frequency || TIER=standard; }
[ -n "${API_KEY:-}" ] || { echo "Set API_KEY to a key with apps:read, apps:write, builds:write and game_servers:write." >&2; exit 2; }
[ -f "$settings" ] || { echo "No settings file: $settings" >&2; exit 2; }

# One call to the API. Prints the JSON reply; on an error prints the problem and stops.
call() {
	local method="$1" path="$2" body="${3:-}" out code
	local args=(-sS -X "$method" "$API$path" -H "Authorization: Bearer $API_KEY" -H "Accept: application/json") # argv-guard: allow (a published sample a studio runs with its own key on its own machine, written the usual way)
	if [ "$method" = POST ] || [ "$method" = PATCH ]; then
		args+=(-H "Idempotency-Key: $(idem "$method$path$body")")
	fi
	[ -n "$body" ] && args+=(-H "Content-Type: application/json" --data "$body")
	out=$(curl "${args[@]}" -w '\n%{http_code}')
	code="${out##*$'\n'}"
	out="${out%$'\n'*}"
	if [ "${code:0:1}" != 2 ]; then
		echo "$method $path failed ($code):" >&2
		[ -n "$out" ] || { echo "  (no body)" >&2; return 1; }
		# (jq's own errors are hidden, its output is not: a reply that is not JSON is printed as it came)
		echo "$out" | jq -er '"  \(.code // "error"): \(.detail // .title // .)"' >&2 2>/dev/null || echo "$out" >&2
		return 1
	fi
	printf '%s' "$out"
}

# The same request made twice (say, after a dropped connection) must not do the work twice: the key is the
# request itself plus this run's ID, so a retry inside the run reuses it.
RUN_ID="$(date +%s)-$$"
idem() { printf '%s %s' "$RUN_ID" "$1" | sha256 | cut -c1-40; }
sha256() { if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi | cut -d' ' -f1; }

# Polls until jq filter $2 on GET $1 prints true; fails on a value matching $3.
wait_for() {
	local path="$1" ok="$2" bad="$3" what="$4" reply
	[ "$PRACTICE" = 1 ] && { echo "  (practice server: not waiting for $what)"; return; }
	for _ in $(seq 1 360); do
		reply=$(call GET "$path")
		[ "$(jq -r "$ok" <<<"$reply")" = true ] && return
		if [ "$(jq -r "$bad" <<<"$reply")" = true ]; then
			echo "$what failed:" >&2; jq . <<<"$reply" >&2; exit 1
		fi
		sleep "$POLL_SECONDS"
	done
	echo "Gave up waiting for $what." >&2; exit 1
}

echo "1. App $APP ($os)"
if ! call GET "/v0/apps/$APP" >/dev/null 2>&1; then
	call POST /v0/apps "$(jq -nc --arg slug "$APP" --arg os "$os" --arg tier "$TIER" --arg continent "$CONTINENT" \
		'{name: $slug, slug: $slug, os: $os, tier: $tier, default_placement: {continents: [$continent]}}')" >/dev/null
fi

echo "2. Build from $source"
if [ "$os" = linux ]; then
	build=$(call POST "/v0/apps/$APP/builds" "$(jq -nc --arg image "$source" '{os: "linux", source: {type: "image", image: $image}}')")
else
	[ -f "$source" ] || { echo "No zip: $source" >&2; exit 2; }
	bytes=$(wc -c <"$source" | tr -d ' ')
	sum=$(sha256 <"$source")
	build=$(call POST "/v0/apps/$APP/builds" "$(jq -nc --arg f "$(basename "$source")" --argjson b "$bytes" --arg s "$sum" \
		'{os: "windows", source: {type: "upload", filename: $f, bytes: $b, sha256: $s}}')")
	part_bytes=$(jq -r '.upload.part_bytes' <<<"$build")
	parts='[]'
	if [ "$(jq '.upload.parts // [] | length' <<<"$build")" = 0 ]; then
		[ "$PRACTICE" = 1 ] || { echo "The API sent no upload parts for the zip:" >&2; jq . <<<"$build" >&2; exit 1; }
		echo "   (practice server: its example has no upload parts; pretending one was uploaded)"
		parts='[{"number": 1, "etag": "\"practice-1\""}]'
	fi
	# Each part goes straight to storage with its exact length (storage links refuse a streamed upload); keep the
	# ETag that comes back.
	while read -r number url; do
		if [ "$PRACTICE" = 1 ]; then
			etag="\"practice-$number\""
		else
			etag=$(dd if="$source" bs="$part_bytes" skip=$((number - 1)) count=1 status=none |
				curl -sS --fail -X PUT --data-binary @- -H 'Content-Type:' -H 'Expect:' -D - -o /dev/null "$url" |
				tr -d '\r' | awk -F': ' 'tolower($1)=="etag"{print $2}')
		fi
		parts=$(jq -c --argjson n "$number" --arg e "$etag" '. + [{number: $n, etag: $e}]' <<<"$parts")
		echo "   part $number uploaded"
	done < <(jq -r '.upload.parts // [] | .[] | "\(.number) \(.url)"' <<<"$build")
	call POST "/v0/builds/$(jq -r .id <<<"$build")/complete" "$(jq -nc --argjson p "$parts" '{parts: $p}')" >/dev/null
fi
build_id=$(jq -r .id <<<"$build")
echo "   $build_id"
wait_for "/v0/builds/$build_id" '.status == "ready"' '.status == "failed"' "the build"

echo "3. Version"
version=$(call POST "/v0/apps/$APP/versions" "$(jq -c --arg b "$build_id" '.build = $b' "$settings")")
name=$(jq -r .name <<<"$version")
echo "   $name"
wait_for "/v0/apps/$APP/versions/$name" 'any(.regions[]; .status == "ready")' 'all(.regions[]; .status == "failed")' "the version to reach a region"

echo "4. Game server"
# A region can read ready a moment before the placement that starts game servers has caught up with it, and the start
# then answers 409 version_not_ready: the same start is tried again for up to a minute (the same Idempotency-Key, so
# it can never start two).
start_body=$(jq -nc --arg app "$APP" --arg v "$name" --arg continent "$CONTINENT" \
	'{app: $app, version: $v, placement: {continents: [$continent]}}')
err=$(mktemp); trap 'rm -f "$err"' EXIT
for wait in 1 2 4 8 15 30 0; do
	if gs=$(call POST /v0/game-servers "$start_body" 2>"$err"); then break; fi
	if [ "$wait" = 0 ] || ! grep -q 'version_not_ready' "$err"; then cat "$err" >&2; exit 1; fi
	echo "   waiting for the image to reach the region ($wait s)..."
	[ "$PRACTICE" = 1 ] || sleep "$wait"
done
id=$(jq -r .id <<<"$gs")
address=$(jq -r '"\(.address.ipv4):\(.address.ports.game.port)"' <<<"$gs")
echo "   $id in $(jq -r .region <<<"$gs"), $(jq -r .status <<<"$gs")"
echo
echo "Players connect to $address"
echo "Try the sample game:  echo ping | nc -u -w1 ${address%:*} ${address##*:}"
echo "Stop it:              curl -X POST $API/v0/game-servers/$id/stop -H \"Authorization: Bearer \$API_KEY\" -H \"Idempotency-Key: stop-$id\""
