# Survival Servers Studio plugin for Unreal Engine: build and release

`SurvivalServersStudio/` is the plugin as a studio drops it into a project's `Plugins` folder; its own README says
how to use it. This folder also holds its tests and the packaging script.

## Test

```bash
tests/run.sh          # the plain C++ rules (no engine): g++ without exceptions or RTTI, as Unreal builds modules
```

The plugin's module builds every file on its own (no precompiled header, no unity build), so a missing include fails
in any project's build. The Unreal sample (`../unreal` in the samples repository) builds with it for Windows and Linux
servers and a Windows client.

## Package

On Windows, with an Unreal Engine (a source build or an installed one) and, for Linux, the engine's cross-compile
toolchain:

```powershell
$env:UE_ROOT = 'C:\UnrealEngine'
.\package.ps1
```

`Packages\UE<major.minor>\` then holds the prebuilt plugin zip (BuildPlugin's output for the editor and for Win64
and Linux games, Development and Shipping), the source zip with `EngineVersion` set to that engine (the file a Fab
code plugin listing takes; Fab compiles it), and `SHA256SUMS.txt`. Built and tested so far with Unreal 5.8.4 from
source. Each older engine needs its own engine build and a package run of its own; an engine built from source takes
about 280 GB of disk, so build one at a time where the disk allows.
