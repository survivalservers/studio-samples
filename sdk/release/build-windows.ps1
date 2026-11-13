# Builds the SDK's Windows libraries with Microsoft's compiler (Visual Studio 2022 or its Build Tools), for the
# Windows release archive (build.sh puts them in it). Run on Windows from a copy of the sdk folder:
#
#   powershell -ExecutionPolicy Bypass -File release\build-windows.ps1 -Out C:\temp\gs-sdk-win
#
# Makes, in -Out:
#   gs_sdk.dll, gs_sdk.lib, gs_sdk.pdb   the shared library and its import library; the C runtime is built in (/MT),
#                                        so the DLL needs nothing else installed
#   gs_sdk_static.lib                    the static library, against the DLL C runtime (/MD) as Unreal and most
#                                        engines build; it links ws2_32 by itself
# Every file compiles at /W4 with warnings as errors.
param([Parameter(Mandatory)][string]$Out)
$ErrorActionPreference = 'Stop'
$sdk = Split-Path -Parent $PSScriptRoot
$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
if (-not (Test-Path $vswhere)) { throw 'Visual Studio 2022 or its Build Tools is not installed' }
$vs = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if (-not $vs) { throw 'No Visual Studio install with the C++ x64 tools' }
$vcvars = Join-Path $vs 'VC\Auxiliary\Build\vcvars64.bat'
if (Test-Path $Out) { throw "$Out already exists: give a new folder" }
New-Item -ItemType Directory -Force -Path $Out | Out-Null
# Object files in a folder of their own beside -Out, removed at the end.
$obj = "$Out-obj"
New-Item -ItemType Directory -Force -Path $obj | Out-Null
$src = Join-Path $sdk 'c\src\gs_sdk.c'
$inc = Join-Path $sdk 'c\include'

function Build([string]$Line) {
    # One cmd per step, with the compiler's environment from vcvars64.
    & cmd.exe /d /c "call `"$vcvars`" >nul && $Line"
    if ($LASTEXITCODE -ne 0) { throw "failed: $Line" }
}
Push-Location $obj
try {
    Build "cl /nologo /c /O2 /W4 /WX /MD /Z7 /I`"$inc`" `"$src`" /Fo:gs_sdk_static.obj"
    Build "lib /nologo /OUT:`"$Out\gs_sdk_static.lib`" gs_sdk_static.obj"
    # /Z7 keeps the debug information in the object file (no PDB server left running); the linker writes gs_sdk.pdb.
    Build "cl /nologo /LD /O2 /W4 /WX /MT /Z7 /DGS_SDK_SHARED /I`"$inc`" `"$src`" /Fe:`"$Out\gs_sdk.dll`" /link /DEBUG /OPT:REF /OPT:ICF /PDB:`"$Out\gs_sdk.pdb`" /IMPLIB:`"$Out\gs_sdk.lib`" ws2_32.lib"
    # What the DLL exports and what it needs: only gs_ functions, and only Windows' own DLLs.
    Build "dumpbin /nologo /exports `"$Out\gs_sdk.dll`" > `"$Out\exports.txt`""
    Build "dumpbin /nologo /dependents `"$Out\gs_sdk.dll`" > `"$Out\dependents.txt`""
} finally { Pop-Location }
Remove-Item -Recurse -Force -LiteralPath $obj -ErrorAction SilentlyContinue
Remove-Item -Force -LiteralPath (Join-Path $Out 'gs_sdk.exp') -ErrorAction SilentlyContinue
$exports = @(Select-String -Path (Join-Path $Out 'exports.txt') -Pattern '\sgs_\w+$').Count
$deps = @(Select-String -Path (Join-Path $Out 'dependents.txt') -Pattern '^\s+\S+\.dll$' | ForEach-Object { $_.Line.Trim() })
if ($exports -lt 20) { throw "gs_sdk.dll exports only $exports gs_ functions" }
$foreign = @($deps | Where-Object { $_ -notmatch '^(KERNEL32|WS2_32)\.dll$' })
if ($foreign.Count -gt 0) { throw "gs_sdk.dll needs $($foreign -join ', ')" }
Remove-Item -Force -LiteralPath (Join-Path $Out 'exports.txt'), (Join-Path $Out 'dependents.txt')
"gs_sdk.dll: $exports gs_ functions; needs $($deps -join ', ')"
Get-ChildItem $Out | ForEach-Object { '{0} {1}' -f $_.Name, $_.Length }
