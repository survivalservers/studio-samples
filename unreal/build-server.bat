@echo off
rem Packages the dedicated server for Windows (zip) and Linux (for the image). Run on Windows from this folder with a
rem source build of Unreal Engine (dedicated server targets need one) and, for Linux, the cross-compile toolchain.
rem   set UE_ROOT=C:\UnrealEngine
rem   build-server.bat
rem The servers are Shipping builds, which have no trace control listener; the cook runs with -notraceserver, and the
rem project keeps the editor's cache server on this machine only (Config/DefaultEngine.ini). -nodebuginfo keeps the
rem debug files (.pdb, .debug, .sym, 1 to 2 GB) out of the packaged servers: they stay in Binaries here, for crashes.
if "%UE_ROOT%"=="" (echo Set UE_ROOT to your Unreal Engine source folder & exit /b 2)
set UAT="%UE_ROOT%\Engine\Build\BatchFiles\RunUAT.bat"
set PROJECT=%~dp0GameServerSample.uproject

rem The Survival Servers Studio plugin goes in Plugins\SurvivalServersStudio, as in any project. In the samples
rem repository it lives in ..\unreal-plugin (..\..\unreal-plugin where the samples sit one folder deeper, as in
rem the source tree they are published from): copy it in (its build files in Binaries and Intermediate are kept).
set PLUGIN_SRC=
if exist "%~dp0..\..\unreal-plugin\SurvivalServersStudio\SurvivalServersStudio.uplugin" set PLUGIN_SRC=%~dp0..\..\unreal-plugin\SurvivalServersStudio
if exist "%~dp0..\unreal-plugin\SurvivalServersStudio\SurvivalServersStudio.uplugin" set PLUGIN_SRC=%~dp0..\unreal-plugin\SurvivalServersStudio
if defined PLUGIN_SRC (
  robocopy "%PLUGIN_SRC%" "%~dp0Plugins\SurvivalServersStudio" /MIR /XD Binaries Intermediate /NFL /NDL /NJH /NJS /NP
  if errorlevel 8 exit /b 1
)
if not exist "%~dp0Plugins\SurvivalServersStudio\SurvivalServersStudio.uplugin" (echo Put the Survival Servers Studio plugin in Plugins\SurvivalServersStudio & exit /b 2)

rem A fresh archive each time: the archive step adds files and never removes old ones.
if exist "%~dp0Builds\WindowsServer" rmdir /s /q "%~dp0Builds\WindowsServer"
call %UAT% BuildCookRun -project="%PROJECT%" -noP4 -server -serverplatform=Win64 -noclient -build -cook -stage -pak -archive -archivedirectory="%~dp0Builds" -serverconfig=Shipping -nodebuginfo -utf8output -AdditionalCookerOptions=-notraceserver || exit /b 1
rem Keep the WindowsServer folder: version-windows.json uses it for the executable, logs and crash files.
powershell -NoProfile -Command "Compress-Archive -Force -Path '%~dp0Builds\WindowsServer' -DestinationPath '%~dp0Builds\GameServerSample-windows.zip'" || exit /b 1
echo Upload Builds\GameServerSample-windows.zip; its executable is WindowsServer\GameServerSampleServer.exe

if exist "%~dp0Builds\LinuxServer" rmdir /s /q "%~dp0Builds\LinuxServer"
call %UAT% BuildCookRun -project="%PROJECT%" -noP4 -server -serverplatform=Linux -noclient -build -cook -stage -pak -archive -archivedirectory="%~dp0Builds" -serverconfig=Shipping -nodebuginfo -utf8output -AdditionalCookerOptions=-notraceserver || exit /b 1
echo Linux server in Builds\LinuxServer; build the image with: docker build -t unreal-server .
