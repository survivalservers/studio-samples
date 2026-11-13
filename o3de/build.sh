#!/usr/bin/env bash
# Builds the sample's server with the O3DE SDK (26.05): O3DE's MinimalProject template made into the project LabServer,
# our game server put into its gem (Gem/Source here), the rendering gems left out (a headless server draws nothing,
# and without them it needs no compiled shaders), then the LabServer.HeadlessServerLauncher target, profile build:
#   build/LabServer/                     the project (made once from the template; our files copied in every build)
#   dist/linux/ or dist/windows/         what a host runs: bin/ (the launcher and the engine's libraries) and project/
#                                        (project.json and the settings the launcher reads); dist/o3de-server-windows.zip
# Run it as bin/LabServer.HeadlessServerLauncher --project-path=../project -rhi=null -bg_ConnectToAssetProcessor=0.
# Needs O3DE (the SDK folder, e.g. /opt/O3DE/26.05), CMake 3.30 or newer and clang (Linux) or Visual Studio 2022's
# C++ tools (Windows, from Git Bash), and O3DE's own -dev packages on Linux (its system requirements page).
set -euo pipefail
cd "$(dirname "$0")"
: "${O3DE:?set O3DE to the O3DE SDK folder}"
here=$PWD proj=$PWD/build/LabServer
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) os=windows o3de="$O3DE/scripts/o3de.bat" ;; *) os=linux o3de="$O3DE/scripts/o3de.sh" ;; esac
[ -f "$proj/project.json" ] || "$o3de" create-project --project-path "$proj" --template-name MinimalProject --project-name LabServer
cp Gem/Source/LabGame.h Gem/Source/LabGame.cpp Gem/Source/LabServerSystemComponent.h Gem/Source/LabServerSystemComponent.cpp \
	"$proj/Gem/Source/"
python3 - "$proj" <<'PY'
import json, re, sys
proj = sys.argv[1]
# The game server's own files in the gem.
p = f"{proj}/Gem/labserver_files.cmake"
s = open(p).read()
if "LabGame.cpp" not in s:
    s = s.replace("Source/LabServerSystemComponent.cpp", "Source/LabServerSystemComponent.cpp\n    Source/LabGame.h\n    Source/LabGame.cpp")
    open(p, "w").write(s)
# Only our gem: Atom (the renderer), the camera and ImGui are for a game that draws.
p = f"{proj}/project.json"
d = json.load(open(p))
d["gem_names"] = ["LabServer"]
json.dump(d, open(p, "w"), indent=4)
p = f"{proj}/Gem/CMakeLists.txt"
s = open(p).read()
s = re.sub(r"\n\s*Gem::Atom_AtomBridge\.Static", "", s)
# LabGame.cpp compiles on its own, not in a unity file after the engine's headers: on Windows winsock2.h must come
# before windows.h.
if "SKIP_UNITY_BUILD_INCLUSION" not in s:
    s += "\nly_add_source_properties(SOURCES Source/LabGame.cpp PROPERTY SKIP_UNITY_BUILD_INCLUSION VALUES ON)\n"
open(p, "w").write(s)
PY
cmake -B "$proj/build/$os" -S "$proj" -G "Ninja Multi-Config" -DLY_3RDPARTY_PATH="$HOME/.o3de/3rdParty"
cmake --build "$proj/build/$os" --target LabServer.HeadlessServerLauncher --config profile
rm -rf "dist/$os" && mkdir -p "dist/$os/bin" "dist/$os/project"
cp -a "$proj/build/$os/bin/profile/." "dist/$os/bin/"
find "dist/$os/bin" -name '*.dbg' -delete
# The SDK's server launchers load the engine's renderer and its tools too (Atom, ImGui, EMotionFX, the camera), and
# Atom stops the launcher when its compiled shaders are not there. A server that draws nothing loads none of them.
python3 - dist/$os/bin/Registry <<'PY'
import glob, json, os, sys
drop = ("Atom", "Camera", "ImGui", "Imgui", "EMotionFX", "CommonFeaturesAtom")
for p in glob.glob(os.path.join(sys.argv[1], "cmake_dependencies.*headlessserverlauncher.setreg")):
    d = json.load(open(p))
    gems = d["O3DE"]["Gems"]
    for g in [g for g in gems if g.startswith(drop)]:
        del gems[g]
    json.dump(d, open(p, "w"), indent=4)
    print(os.path.basename(p), "loads", ", ".join(gems))
PY
cp -a "$proj/project.json" "$proj/Registry" "dist/$os/project/"
rm -f "dist/$os/project/Registry/load_level.setreg" # the template's "defaultlevel": a server with no level loads none
[ -d "$proj/Gem" ] && mkdir -p "dist/$os/project/Gem" && cp "$proj/Gem/gem.json" "dist/$os/project/Gem/"
if [ "$os" = windows ]; then
	(cd "dist/$os" && python3 -m zipfile -c "$here/dist/o3de-server-windows.zip" bin project)
fi
du -sh "dist/$os"
