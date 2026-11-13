// The sample's game mode: the plugin's subsystem does the platform's work on its own; this shows the calls a game
// adds, a label for your backend and a save when a shutdown notice arrives.
#pragma once

#include "CoreMinimal.h"
#include "GameFramework/GameModeBase.h"
#include "GameServerSampleGameMode.generated.h"

UCLASS()
class AGameServerSampleGameMode : public AGameModeBase
{
	GENERATED_BODY()

protected:
	virtual void BeginPlay() override;

private:
	UFUNCTION()
	void OnMatchAllocated(const FString& MatchId);

	UFUNCTION()
	void OnStopRequested(const FString& Reason, FDateTime Deadline);
};
