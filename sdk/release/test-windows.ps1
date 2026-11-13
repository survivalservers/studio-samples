# Tests the Windows archive in a new, empty folder: unpacks it, builds the C (static and DLL), C++ and C# samples
# against it with Microsoft's compiler and a .NET 8 SDK installed into that folder by Microsoft's own install script,
# and the Go and Rust samples with the toolchains in -Tools (Go and MinGW-w64 for cgo; Rust with the MSVC target),
# and runs each under the archive's survivalservers dev, checking every call as the repository's own SDK test does. Needs
# Visual Studio 2022 or its Build Tools. Without -Tools (or with a toolchain missing there) Go and Rust fail.
param([Parameter(Mandatory)][string]$Zip, [string]$Tools = 'C:\gstools')
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$dir = Split-Path -Parent $Zip
Add-Type -AssemblyName System.IO.Compression.FileSystem
[IO.Compression.ZipFile]::ExtractToDirectory($Zip, $dir)
$root = Get-ChildItem -Directory $dir | Where-Object { $_.Name -like 'survival-servers-studio-sdk-*-windows-x64' } | Select -First 1
Set-Location $root.FullName
"archive: $(Get-Content VERSION)"

$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
$vs = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
$vcvars = Join-Path $vs 'VC\Auxiliary\Build\vcvars64.bat'
function Build([string]$Line) {
    $out = & cmd.exe /d /c "call `"$vcvars`" >nul && $Line" 2>&1
    if ($LASTEXITCODE -ne 0) { $out; throw "failed: $Line" }
}

# .NET 8 SDK into this folder only, from Microsoft's install script.
$dotnetDir = Join-Path $dir 'dotnet'
Invoke-WebRequest -UseBasicParsing -Uri 'https://dot.net/v1/dotnet-install.ps1' -OutFile (Join-Path $dir 'dotnet-install.ps1')
$sig = Get-AuthenticodeSignature (Join-Path $dir 'dotnet-install.ps1')
if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notmatch 'Microsoft') { throw "dotnet-install.ps1 is not signed by Microsoft: $($sig.Status)" }
& (Join-Path $dir 'dotnet-install.ps1') -Channel 8.0 -InstallDir $dotnetDir -NoPath | Out-Null
$env:DOTNET_ROOT = $dotnetDir
$env:PATH = "$dotnetDir;$env:PATH"
$env:DOTNET_CLI_TELEMETRY_OPTOUT = '1'
$env:DOTNET_NOLOGO = '1'
$cl = & cmd.exe /d /c "call `"$vcvars`" >nul && cl 2>&1" | Select-String -Pattern 'Version' | Select -First 1
"toolchains: $($cl.Line.Trim()); dotnet $(& dotnet --version)"

$script:fails = 0
function Check([string]$Name, [string]$Program) {
    $state = Join-Path $dir ("agent-" + [guid]::NewGuid().ToString('N'))
    # survivalservers dev runs the program in its -dir folder: give it the full path.
    $Program = (Resolve-Path $Program).Path
    $out = (& .\tools\survivalservers.exe dev -port 0 -warm -allocate-after 300ms -env MAP=dunes -external-id m42 -shutdown-time 5s `
            -stop local_api -dir $state -- $Program 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0) { "FAIL ${Name}: survivalservers dev exit $LASTEXITCODE"; $out; $script:fails++; return }
    foreach ($want in 'match m42 map=dunes', 'players: 1 [p1]', 'players: 2 [p1 p2]', 'refused a player ID with a control character',
        'labels: mode=ffa', 'health', 'tick: p99 6.25 ms p50 2.50 ms at 64 Hz', 'the server asked to stop: "match_over"',
        'shutdown reason=self deadline=set', 'exited after the stop request with code 0') {
        if (-not $out.Contains($want)) { "FAIL ${Name}: no `"$want`""; $out; $script:fails++; return }
    }
    "PASS $Name"
}

Build 'cl /nologo /W4 /WX /MD /Iinclude samples\c\sample.c lib\gs_sdk_static.lib /Fe:sample-c.exe'
Check 'C, static library' '.\sample-c.exe'
New-Item -ItemType Directory -Force -Path dll | Out-Null
Build 'cl /nologo /W4 /WX /DGS_SDK_SHARED /Iinclude samples\c\sample.c lib\gs_sdk.lib /Fe:dll\sample-c-dll.exe'
Copy-Item lib\gs_sdk.dll dll\
Check 'C, DLL' '.\dll\sample-c-dll.exe'
Build 'cl /nologo /W4 /WX /EHsc /std:c++17 /MD /Iinclude samples\cpp\sample.cpp lib\gs_sdk_static.lib /Fe:sample-cpp.exe'
Check 'C++' '.\sample-cpp.exe'
$b = & dotnet build samples\csharp -c Release -o out\csharp --nologo -v q 2>&1
if ($LASTEXITCODE -ne 0) { $b; throw 'dotnet build failed' }
Check 'C#' '.\out\csharp\Sample.exe'

# Go and Rust compile the C library from the archive's source themselves (cgo with MinGW-w64; the cc crate with MSVC).
foreach ($need in 'go\bin\go.exe', 'mingw64\bin\gcc.exe', 'cargo\bin\cargo.exe') {
    if (-not (Test-Path (Join-Path $Tools $need))) { "FAIL: no $need in $Tools"; $script:fails++ }
}
if ($script:fails -eq 0) {
    $env:PATH = "$Tools\go\bin;$Tools\mingw64\bin;$Tools\cargo\bin;$env:PATH"
    $env:RUSTUP_HOME = "$Tools\rustup"
    $env:CARGO_HOME = "$Tools\cargo"
    $env:GOPATH = Join-Path $dir 'gopath'
    $env:GOCACHE = Join-Path $dir 'gocache'
    $env:GOFLAGS = '-mod=mod'
    $env:CGO_ENABLED = '1'
    "toolchains: $(& go version); $((& gcc --version) | Select -First 1); $(& cargo --version)"
    # cgo compiles with MinGW-w64's gcc; Rust's MSVC target must not see CC (its cc crate would use gcc too, and
    # MSVC's linker cannot link that object).
    Push-Location samples\go
    $env:CC = 'gcc'
    $b = & go build -o ..\..\sample-go.exe . 2>&1
    $rc = $LASTEXITCODE
    Remove-Item Env:CC
    Pop-Location
    if ($rc -ne 0) { $b; throw 'go build failed' }
    Check 'Go' '.\sample-go.exe'
    Push-Location samples\rust
    $b = & cmd.exe /d /c "cargo build --release -q --target-dir ..\..\rust-target 2>&1"
    $rc = $LASTEXITCODE
    Pop-Location
    if ($rc -ne 0) { $b; throw 'cargo build failed' }
    Check 'Rust' '.\rust-target\release\gs-sample.exe'
}
if ($script:fails -ne 0) { throw "$($script:fails) sample(s) failed" }
'all samples built against the archive and passed'
