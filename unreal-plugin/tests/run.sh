#!/usr/bin/env bash
# Builds and runs the plain C++ tests with the system compiler (no Unreal needed), without exceptions or RTTI,
# as Unreal builds modules by default.
set -euo pipefail
cd "$(dirname "$0")"
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
"${CXX:-g++}" -std=c++17 -Wall -Wextra -Werror -O1 -fno-exceptions -fno-rtti -fsanitize=address,undefined \
	-o "$out/core_test" core_test.cpp ../SurvivalServersStudio/Source/SurvivalServersStudio/Private/Core/GameServerCore.cpp
"$out/core_test"

# No stand-in link ships in the plugin (package.ps1 refuses one too): the editor shows the descriptor's links.
if grep -rnIE '://([a-z0-9-]+\.)*example\.com' ../SurvivalServersStudio; then
	echo "^ a stand-in address in the plugin: name the real one"; exit 1
fi
