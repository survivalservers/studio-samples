#include "GameServerSampleGameMode.h"

#include "Engine/GameInstance.h"
#include "Engine/World.h"
#include "SurvivalServersSubsystem.h"

void AGameServerSampleGameMode::BeginPlay()
{
	Super::BeginPlay();
	UGameInstance* GameInstance = GetGameInstance();
	USurvivalServersSubsystem* Server = GameInstance ? GameInstance->GetSubsystem<USurvivalServersSubsystem>() : nullptr;
	if (!Server) return; // not a dedicated server
	Server->OnMatchAllocated.AddDynamic(this, &AGameServerSampleGameMode::OnMatchAllocated);
	Server->OnStopRequested.AddDynamic(this, &AGameServerSampleGameMode::OnStopRequested);
	Server->SetLabel(TEXT("map"), GetWorld()->GetMapName());
	// A ready server's match comes later; a fresh start already has it.
	if (!Server->GetMatchId().IsEmpty()) OnMatchAllocated(Server->GetMatchId());
}

void AGameServerSampleGameMode::OnMatchAllocated(const FString& MatchId)
{
	USurvivalServersSubsystem* Server = GetGameInstance()->GetSubsystem<USurvivalServersSubsystem>();
	FString Mode;
	if (!Server->GetSetting(TEXT("MODE"), Mode)) Mode = TEXT("default");
	UE_LOG(LogSurvivalServers, Display, TEXT("sample: match %s, mode %s"), *MatchId, *Mode);
}

void AGameServerSampleGameMode::OnStopRequested(const FString& Reason, FDateTime Deadline)
{
	// A real game saves here, before the deadline. The plugin then ends the match and exits.
	UE_LOG(LogSurvivalServers, Display, TEXT("sample: stop asked (%s), deadline %s"), *Reason, *Deadline.ToIso8601());
}
