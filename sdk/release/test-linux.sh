#!/usr/bin/env bash
# Runs inside a fresh Ubuntu 24.04 container with the Linux archive in /work: installs the
# toolchains a studio would, unpacks the archive, builds every sample against it (C static and shared, C++, C#, Go,
# Rust) and runs each under the archive's survivalservers dev, checking every call as the repository's own SDK test does.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1
apt-get update -q >/dev/null
apt-get install -y -q --no-install-recommends ca-certificates curl gcc g++ libc6-dev golang-go dotnet-sdk-8.0 >/dev/null
# Rust from rustup, the way its own site installs it; minimal profile.
curl -sSf --proto '=https' --tlsv1.2 https://sh.rustup.rs | sh -s -- -y -q --profile minimal >/dev/null
. "$HOME/.cargo/env"
echo "toolchains: $(gcc --version | head -1); $(go version); dotnet $(dotnet --version); $(cargo --version)"

cd /work
tar -xzf survival-servers-studio-sdk-*-linux-x64.tar.gz
cd survival-servers-studio-sdk-*-linux-x64
echo "archive: $(cat VERSION)"
fails=0
grep -q '^==== Go go' tools/THIRD_PARTY_NOTICES.txt && echo "PASS third-party notices beside the tool" ||
	{ echo "FAIL third-party notices: none beside the tool"; fails=$((fails + 1)); }
check() { # name program...
	local name=$1 out prog=$2
	shift 2
	# survivalservers dev runs the program in its -dir folder: give it the full path.
	[[ $prog = /* ]] || prog=$PWD/$prog
	out=$(timeout 60 ./tools/survivalservers dev -port 0 -warm -allocate-after 300ms -env MAP=dunes -external-id m42 -shutdown-time 5s \
		-stop local_api -dir "$(mktemp -d)" -- "$prog" "$@" 2>&1) || { echo "FAIL $name: survivalservers dev exit $?"; echo "$out"; fails=$((fails + 1)); return; }
	for want in "match m42 map=dunes" "players: 1 [p1]" "players: 2 [p1 p2]" "refused a player ID with a control character" \
		"labels: mode=ffa" "health" "tick: p99 6.25 ms p50 2.50 ms at 64 Hz" 'the server asked to stop: "match_over"' \
		"shutdown reason=self deadline=set" "exited after the stop request with code 0"; do
		if ! grep -qF -- "$want" <<<"$out"; then echo "FAIL $name: no \"$want\""; echo "$out"; fails=$((fails + 1)); return; fi
	done
	echo "PASS $name"
}

cc -std=c99 -Wall -Wextra -Werror -Iinclude samples/c/sample.c lib/libgs_sdk.a -lpthread -o sample-c
check "C, static library" ./sample-c
cc -std=c99 -Wall -Wextra -Werror -DGS_SDK_SHARED -Iinclude samples/c/sample.c -Llib -lgs_sdk -Wl,-rpath,'$ORIGIN/lib' -o sample-c-shared
check "C, shared library" ./sample-c-shared
c++ -std=c++17 -Wall -Wextra -Werror -Iinclude samples/cpp/sample.cpp lib/libgs_sdk.a -lpthread -o sample-cpp
check "C++" ./sample-cpp
dotnet build samples/csharp -c Release -o out/csharp --nologo -v q >/dev/null
check "C#" out/csharp/Sample
(cd samples/go && go build -o ../../sample-go .)
check "Go" ./sample-go
(cd samples/rust && cargo build --release -q)
check "Rust" samples/rust/target/release/gs-sample
[ "$fails" = 0 ] || { echo "$fails sample(s) failed"; exit 1; }
echo "all samples built against the archive and passed"
