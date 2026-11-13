#!/usr/bin/env bash
# From this folder to a running game server, and the address players connect to.
#
#   export GS_API_URL=https://studio.survivalservers.com GS_API_KEY=gsk_live_...
#   ./deploy.sh windows     needs curl, unzip, zip and sha512sum (no Godot export templates)
#   ./deploy.sh linux       needs Docker
#
# Needs survivalservers, the API's address in GS_API_URL (there is no default yet), and a key with apps:read,
# apps:write, builds:write and game_servers:write.
# Optional: APP (default godot-starter-<system>), CONTINENT (default europe), VERSION, GODOT_VERSION.
set -euo pipefail
cd "$(dirname "$0")"

os="${1:-windows}"
[ "$os" = windows ] || [ "$os" = linux ] || { sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
[ -n "${GS_API_URL:-}" ] || { echo "set GS_API_URL=https://studio.survivalservers.com, the API's address" >&2; exit 2; }
APP="${APP:-godot-starter-$os}"
CONTINENT="${CONTINENT:-europe}"
VERSION="${VERSION:-1.0.$(date +%s)}"
GODOT_VERSION="${GODOT_VERSION:-4.7.2}"

survivalservers apps get "$APP" >/dev/null 2>&1 || survivalservers apps create "$APP" --os "$os" --continent "$CONTINENT"

if [ "$os" = windows ]; then
	# Godot's own Windows program with the project beside it: nothing to export, and it builds on any system.
	mkdir -p .build
	release="Godot_v${GODOT_VERSION}-stable_win64.exe"
	from="https://github.com/godotengine/godot/releases/download/${GODOT_VERSION}-stable"
	[ -f ".build/$release.zip" ] || curl -fsSL -o ".build/$release.zip" "$from/$release.zip"
	# Checked against the sums Godot publishes with the release, every time (a cached copy too).
	curl -fsSL -o ".build/SHA512-SUMS-$GODOT_VERSION.txt" "$from/SHA512-SUMS.txt"
	(cd .build && grep " $release.zip\$" "SHA512-SUMS-$GODOT_VERSION.txt" | sha512sum -c --quiet -) ||
		{ echo "$release.zip does not match Godot's published SHA-512: delete .build/$release.zip and try again" >&2; exit 1; }
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	unzip -q ".build/$release.zip" -d "$work"
	cp -r project.godot main.tscn main.gd addons "$work/"
	(cd "$work" && zip -q -r server.zip .)
	# The version runs the console program of this GODOT_VERSION, whatever version-windows.json names.
	sed "s/Godot_v[^\"]*-stable_win64_console\.exe/Godot_v${GODOT_VERSION}-stable_win64_console.exe/" version-windows.json > "$work/version-windows.json"
	survivalservers deploy "$APP" --zip "$work/server.zip" --settings "$work/version-windows.json" --name "$VERSION" --branch live
else
	repo=$(survivalservers registry login | sed -n 's|^Push to \(.*\)/<server>.*|\1|p')
	image="$repo/godot-starter:$VERSION"
	docker build --build-arg "GODOT_VERSION=$GODOT_VERSION" -t "$image" .
	docker push "$image"
	survivalservers deploy "$APP" --image "$image" --settings version-linux.json --name "$VERSION" --branch live
fi

survivalservers game-servers start "$APP" --branch live --continent "$CONTINENT" --wait
