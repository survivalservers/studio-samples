# Tests the actual ZIP command's paths against version-windows.json using a representative staged build.
# No Unreal installation/build and no network access. Run with pwsh or Windows PowerShell 5.1.
$ErrorActionPreference = 'Stop'
$sample = Split-Path -Parent $PSScriptRoot
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('gs-unreal-archive-' + [guid]::NewGuid().ToString('N'))

function Assert-Layout([bool]$Ok, [string]$Message) {
    if (-not $Ok) { throw $Message }
}

function Resolve-BatchPath([string]$Value) {
    $root = $fixture + [IO.Path]::DirectorySeparatorChar
    $Value.Replace('%~dp0', $root).Replace('\', [IO.Path]::DirectorySeparatorChar)
}

try {
    $staged = @(
        'Builds/WindowsServer/GameServerSampleServer.exe',
        'Builds/WindowsServer/GameServerSample/Binaries/Win64/GameServerSampleServer-Win64-Shipping.exe',
        'Builds/WindowsServer/GameServerSample/Saved/Logs/server.log',
        'Builds/WindowsServer/GameServerSample/Saved/Crashes/test/context.txt',
        'Builds/LinuxServer/GameServerSampleServer.sh'
    )
    foreach ($relative in $staged) {
        $path = Join-Path $fixture $relative
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
        [IO.File]::WriteAllText($path, 'archive layout fixture')
    }
    $batch = Get-Content (Join-Path $sample 'build-server.bat') -Raw
    $command = [regex]::Match($batch, "Compress-Archive -Force -Path '([^']+)' -DestinationPath '([^']+)'")
    Assert-Layout $command.Success 'The Windows packaging command could not be read'
    $source = Resolve-BatchPath $command.Groups[1].Value
    $zip = Resolve-BatchPath $command.Groups[2].Value
    Compress-Archive -Force -Path $source -DestinationPath $zip

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($zip)
    try { $entries = @($archive.Entries | ForEach-Object { $_.FullName.Replace('\', '/') }) }
    finally { $archive.Dispose() }
    $version = Get-Content (Join-Path $sample 'version-windows.json') -Raw | ConvertFrom-Json
    Assert-Layout ($entries -contains $version.launch.executable) ("ZIP is missing launch executable " + $version.launch.executable)
    foreach ($pattern in @($version.launch.log_paths) + @($version.launch.artifact_paths)) {
        Assert-Layout (@($entries | Where-Object { $_ -like $pattern }).Count -gt 0) ("ZIP has no fixture matching " + $pattern)
    }
    Assert-Layout (@($entries | Where-Object { $_ -notlike 'WindowsServer/*' }).Count -eq 0) 'ZIP contains files outside its WindowsServer folder'
    $unpacked = Join-Path $fixture 'unpacked'
    Expand-Archive -Path $zip -DestinationPath $unpacked
    Assert-Layout (Test-Path (Join-Path $unpacked $version.launch.executable) -PathType Leaf) 'Extracted launch path does not exist'
    foreach ($entry in $entries | Where-Object { -not $_.EndsWith('/') }) {
        Assert-Layout (Test-Path (Join-Path $unpacked $entry) -PathType Leaf) ("Extracted fixture is missing " + $entry)
    }
    'archive_layout_test: launch, log and crash paths match the extracted Windows ZIP; no Linux build included'
} finally {
    if (Test-Path $fixture) { Remove-Item -Recurse -Force -LiteralPath $fixture }
}
