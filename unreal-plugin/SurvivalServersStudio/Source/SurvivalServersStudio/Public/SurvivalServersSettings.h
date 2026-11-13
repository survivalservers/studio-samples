// Project Settings > Plugins > Survival Servers Studio. Saved in Config/DefaultGame.ini under
// [/Script/SurvivalServersStudio.SurvivalServersSettings], which a packaged server reads.
#pragma once

#include "CoreMinimal.h"
#include "Engine/DeveloperSettings.h"
#include "SurvivalServersSettings.generated.h"

UCLASS(Config = Game, DefaultConfig, meta = (DisplayName = "Survival Servers Studio"))
class SURVIVALSERVERSSTUDIO_API USurvivalServersSettings : public UDeveloperSettings
{
	GENERATED_BODY()

public:
	USurvivalServersSettings();

	virtual FName GetCategoryName() const override { return TEXT("Plugins"); }

	// Say ready once the server's map is loaded and it listens. Turn off to call Ready yourself, for example after
	// loading your own data.
	UPROPERTY(Config, EditAnywhere, Category = "Game server")
	bool bReadyWhenMapLoaded = true;

	// Report players from the game mode's logins and logouts. Turn off to call PlayerConnect and PlayerDisconnect
	// (or SetPlayerCount) yourself.
	UPROPERTY(Config, EditAnywhere, Category = "Game server")
	bool bReportPlayersFromLogins = true;

	// End a match once everyone who joined has left. Matches only: a persistent server never ends itself this way.
	UPROPERTY(Config, EditAnywhere, Category = "Game server")
	bool bStopWhenLastPlayerLeaves = false;

	// When a shutdown notice arrives, broadcast On Stop Requested, then end the match and exit. Turn off to save and
	// exit in your own time (call Stop, or exit), and be done by halfway through the version's stop grace: because
	// this plugin waits for the notice, the version's stop signal is held back until then and arrives only if the
	// server still runs (a local_api stop sends none). At the notice's deadline the whole process tree is killed.
	UPROPERTY(Config, EditAnywhere, Category = "Game server")
	bool bExitOnStopRequest = true;

	// Send the server's work time per frame (the frame minus the time it slept) once a second, for the tick graph.
	UPROPERTY(Config, EditAnywhere, Category = "Game server")
	bool bReportFrameTimes = true;
};
