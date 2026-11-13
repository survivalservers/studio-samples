# Packages the Survival Servers Studio plugin for one Unreal Engine version, ready to publish (nothing is uploaded):
#
#   $env:UE_ROOT = 'C:\UnrealEngine'        # a source build or an installed engine (tested: 5.8.4 from source)
#   .\package.ps1                           # Win64 and Linux (Linux needs the engine's cross-compile toolchain)
#   .\package.ps1 -TargetPlatforms Win64    # Windows only
#
# Writes to Packages\UE<major.minor>\:
#   SurvivalServersStudio-<version>-UE<major.minor>.zip       the plugin as BuildPlugin packaged it, with prebuilt
#                                                             binaries: unzip into a project's Plugins folder
#   SurvivalServersStudio-<version>-UE<major.minor>-source.zip  source only, its EngineVersion set to this engine: the
#                                                             file a Fab code plugin listing takes (Fab compiles it)
#   SHA256SUMS.txt                                            each zip's SHA-256
#   buildplugin.log                                           RunUAT's output
# BuildPlugin compiles the plugin for the editor on this machine and for Development and Shipping games on each
# target platform. The module always builds each file on its own (no precompiled header, no unity build: its
# Build.cs), so a missing include fails here, not in a studio's project. BuildPlugin's own -StrictIncludes is used
# only with an installed engine: on a source build it rebuilds the whole editor that way.
param(
    [string]$EngineRoot = $env:UE_ROOT,
    [string]$TargetPlatforms = 'Win64+Linux',
    [string]$Out = (Join-Path $PSScriptRoot 'Packages')
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
# Zips with forward slashes and the plugin's folder at the top. Windows PowerShell's Compress-Archive and
# ZipFile.CreateFromDirectory both write backslashes there, which other systems read as part of a file name.
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
function Zip([string]$Folder, [string]$Zip) {
    $base = Split-Path -Parent $Folder
    $archive = [IO.Compression.ZipFile]::Open($Zip, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($file in Get-ChildItem -Recurse -File -LiteralPath $Folder) {
            $entry = $file.FullName.Substring($base.Length + 1).Replace('\', '/')
            $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, $file.FullName, $entry, [IO.Compression.CompressionLevel]::Optimal)
        }
    } finally { $archive.Dispose() }
}
if (-not $EngineRoot) { throw 'Set UE_ROOT (or -EngineRoot) to your Unreal Engine folder' }
$uat = Join-Path $EngineRoot 'Engine\Build\BatchFiles\RunUAT.bat'
if (-not (Test-Path $uat)) { throw "No RunUAT.bat under $EngineRoot" }
$plugin = Join-Path $PSScriptRoot 'SurvivalServersStudio'
$descriptor = Get-Content (Join-Path $plugin 'SurvivalServersStudio.uplugin') -Raw | ConvertFrom-Json
# No stand-in link ships (docs.example.com and the like): the editor shows the descriptor's links and Fab checks them.
$standIns = Get-ChildItem -Recurse -File -LiteralPath $plugin | Select-String -Pattern '://([a-z0-9-]+\.)*example\.com' -List
if ($standIns) { throw "Stand-in addresses in the plugin: $(($standIns | ForEach-Object { "$($_.Path):$($_.LineNumber)" }) -join ', ')" }
$build = Get-Content (Join-Path $EngineRoot 'Engine\Build\Build.version') -Raw | ConvertFrom-Json
$engine = "$($build.MajorVersion).$($build.MinorVersion)"
if ([version]$engine -lt [version]'5.4') { throw "Unreal $engine is older than 5.4: this script packages 5.4 or newer, and only 5.8 is built and tested" }
if ([version]$engine -lt [version]'5.8') { Write-Warning "Unreal $engine: only 5.8 is built and tested with this plugin" }
$name = "SurvivalServersStudio-$($descriptor.VersionName)-UE$engine"
$dir = Join-Path $Out "UE$engine"
$staging = Join-Path $dir 'staging'
# A fresh output folder each time: BuildPlugin refuses to package into a folder holding the plugin's own source.
if (Test-Path $dir) { Remove-Item -Recurse -Force -LiteralPath $dir }
New-Item -ItemType Directory -Force -Path $staging | Out-Null
$log = Join-Path $dir 'buildplugin.log'

# 1. BuildPlugin, from a copy of the source with no build files, into a folder of its own.
$source = Join-Path $staging 'source\SurvivalServersStudio'
New-Item -ItemType Directory -Force -Path $source | Out-Null
foreach ($item in 'Config', 'Resources', 'Source', 'LICENSE', 'README.md', 'SurvivalServersStudio.uplugin') {
    Copy-Item -Recurse -Path (Join-Path $plugin $item) -Destination $source
}
$packaged = Join-Path $staging 'packaged\SurvivalServersStudio'
$argv = @('BuildPlugin', "-Plugin=$(Join-Path $source 'SurvivalServersStudio.uplugin')", "-Package=$packaged",
    "-TargetPlatforms=$TargetPlatforms", '-NoP4', '-utf8output')
if (Test-Path (Join-Path $EngineRoot 'Engine\Build\InstalledBuild.txt')) { $argv += '-StrictIncludes' }
$p = Start-Process -FilePath $uat -ArgumentList $argv -NoNewWindow -PassThru -RedirectStandardOutput $log -RedirectStandardError "$log.err"
$null = $p.Handle # without this PowerShell never fills in ExitCode
try { $p.PriorityClass = 'BelowNormal' } catch { }
$p.WaitForExit()
Get-Content "$log.err" -ErrorAction SilentlyContinue | Add-Content $log
Remove-Item "$log.err" -ErrorAction SilentlyContinue
if ($p.ExitCode -ne 0) { throw "BuildPlugin failed (exit $($p.ExitCode)); see $log" }
foreach ($platform in $TargetPlatforms.Split('+')) {
    if (-not (Get-ChildItem -Recurse -File -Path $packaged | Where-Object { $_.FullName -match "\\$platform\\" })) {
        throw "BuildPlugin made no $platform files"
    }
}

# 2. The prebuilt zip, with the plugin's folder at its top.
$prebuilt = Join-Path $dir "$name.zip"
Zip $packaged $prebuilt

# 3. The source zip for Fab: the source copy with EngineVersion set to this engine.
$text = Get-Content (Join-Path $source 'SurvivalServersStudio.uplugin') -Raw
$text = $text -replace '"EngineVersion":\s*"[^"]*"', ('"EngineVersion": "' + $engine + '.0"')
[IO.File]::WriteAllText((Join-Path $source 'SurvivalServersStudio.uplugin'), $text)
$sourceZip = Join-Path $dir "$name-source.zip"
Zip $source $sourceZip

$sums = foreach ($zip in $prebuilt, $sourceZip) {
    '{0}  {1}' -f (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant(), (Split-Path -Leaf $zip)
}
[IO.File]::WriteAllLines((Join-Path $dir 'SHA256SUMS.txt'), [string[]]$sums)
Remove-Item -Recurse -Force -LiteralPath $staging
$sums
"Packaged for Unreal $engine ($TargetPlatforms) in $dir"
