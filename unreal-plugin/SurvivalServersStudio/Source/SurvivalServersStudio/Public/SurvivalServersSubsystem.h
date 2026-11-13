// The platform's optional calls for an Unreal dedicated server, in one game-instance subsystem. Nothing here is
// needed to run: a server started with -Port={{port.game}} works as it is. This adds: ready once the map is loaded and
// the server listens, the match and its settings, the player list from logins and logouts, frame times for the tick
// graph, the shutdown notice, a self-stop when the match is over, and a result file. Created only in dedicated servers;
// without a local API (a run on your own PC) every call does nothing.
#pragma once

#include "CoreMinimal.h"
#include "Containers/Ticker.h"
#include "Subsystems/GameInstanceSubsystem.h"
#include "SurvivalServersSubsystem.generated.h"

class AController;
class AGameModeBase;
class APlayerController;
class UWorld;

SURVIVALSERVERSSTUDIO_API DECLARE_LOG_CATEGORY_EXTERN(LogSurvivalServers, Log, All);

// A game server's match, from WaitAllocation.
struct FSurvivalServersAllocation
{
	FString GameServerId;
	FString ExternalId;             // the match ID your backend gave the start call
	TMap<FString, FString> Env;     // the start's settings, secret values included
	TMap<FString, FString> Labels;
	TArray<FString> Args;
};

// A shutdown notice, from WaitShutdown: save and exit before the deadline.
struct FSurvivalServersShutdown
{
	FDateTime Deadline;
	FString Reason;                 // api, bulk, self, ttl, max_duration, health_timeout, restart and others
};

DECLARE_DYNAMIC_MULTICAST_DELEGATE_OneParam(FSurvivalServersMatchAllocated, const FString&, MatchId);
DECLARE_DYNAMIC_MULTICAST_DELEGATE_TwoParams(FSurvivalServersStopRequested, const FString&, Reason, FDateTime, Deadline);

UCLASS()
class SURVIVALSERVERSSTUDIO_API USurvivalServersSubsystem : public UGameInstanceSubsystem
{
	GENERATED_BODY()

public:
	virtual bool ShouldCreateSubsystem(UObject* Outer) const override;
	virtual void Initialize(FSubsystemCollectionBase& Collection) override;
	virtual void Deinitialize() override;

	// The match arrived (on a fresh start, before ready; on a ready server, when a match takes it). Its settings are
	// in GetSetting.
	UPROPERTY(BlueprintAssignable, Category = "Survival Servers")
	FSurvivalServersMatchAllocated OnMatchAllocated;

	// A shutdown notice arrived: save now. Unless the project settings say otherwise, the match then ends and the
	// server exits.
	UPROPERTY(BlueprintAssignable, Category = "Survival Servers")
	FSurvivalServersStopRequested OnStopRequested;

	// The calls, under the same names as the custom-engine SDK (C gs_ready, C# Ready() and so on), on Unreal's own
	// non-blocking HTTP. The subsystem already says ready when the map is loaded, waits for the match and the stop
	// notice, reports players and frame times; these are for anything more.

	// Ready for players (the local_api ready check). Done gets whether the platform took it.
	void Ready(TFunction<void(bool bOk)> Done = nullptr);
	// Waits up to WaitSeconds (at most 60) for the match; Done gets null when there is none yet (or a stop came
	// first): call again.
	void WaitAllocation(int32 WaitSeconds, TFunction<void(const FSurvivalServersAllocation*)> Done);
	// Waits up to WaitSeconds (at most 60) for a shutdown notice; Done gets null when there is none.
	void WaitShutdown(int32 WaitSeconds, TFunction<void(const FSurvivalServersShutdown*)> Done);

	UFUNCTION(BlueprintCallable, Category = "Survival Servers", meta = (DisplayName = "Ready"))
	void K2_Ready() { Ready(); }

	// Still healthy, for a version's health_timeout.
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void Health();

	// Ends the match: the last player report, the result file, the self-stop, then the exit.
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void Stop(const FString& Reason);

	// The player list is kept here and sent whole after each change, so the count the platform sees (which also
	// tells a persistent server when nobody plays) is never zero while someone does.
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void PlayerConnect(const FString& Id);
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void PlayerDisconnect(const FString& Id);
	// For games without player IDs: how many are playing.
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void SetPlayerCount(int32 Count);

	// A label your backend filters on (map, mode, open slots).
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void SetLabel(const FString& Name, const FString& Value);
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void RemoveLabel(const FString& Name);

	// Frame times for the last second, once a second, for the tick graph; Hz 0 leaves the rate out. The subsystem
	// sends these by itself unless the project settings turn it off.
	UFUNCTION(BlueprintCallable, Category = "Survival Servers", meta = (DisplayName = "Report Frame Times"))
	void Tick(double P50Ms, double P99Ms, int32 Hz = 0);

	// A persistent server just finished a save: it is backed up at once and, with Keep, kept under that name.
	// Optional: saves are backed up without it.
	UFUNCTION(BlueprintCallable, Category = "Survival Servers")
	void Saved(const FString& Keep = TEXT(""));

	// True on a hosted game server (the local API is there); false in a run on your own PC.
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	bool IsHosted() const;
	// True on a persistent server, false on a match (and on your own PC).
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	bool IsPersistentServer() const;
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	FString GetGameServerId() const;
	// The match ID your backend gave the start call; empty until the match arrives.
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	FString GetMatchId() const { return Allocation.ExternalId; }
	// The address and port players connect to.
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	FString GetPublicIp() const;
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	int32 GetPublicPort() const;
	// One of the match's settings (from the start call, secret values included). False when it is not set.
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	bool GetSetting(const FString& Name, FString& Value) const;
	// How many players the subsystem has reported.
	UFUNCTION(BlueprintPure, Category = "Survival Servers")
	int32 GetPlayerCount() const;

	// The whole match, for C++.
	const FSurvivalServersAllocation& GetAllocation() const { return Allocation; }

private:
	struct FState;

	void OnMapLoaded(UWorld* World);
	void OnPostLogin(AGameModeBase* GameMode, APlayerController* NewPlayer);
	void OnLogout(AGameModeBase* GameMode, AController* Exiting);
	bool OnFrame(float DeltaSeconds);

	// One call to the local API; Done gets the HTTP status (0 when the call did not complete) and the body.
	void Call(const FString& Verb, const FString& Url, const FString& Body, TFunction<void(int32, const FString&)> Done);
	void Send(const FString& Verb, const FString& Path, const FString& Body);
	void PollAllocation();
	void PollShutdown();
	void After(float Seconds, TFunction<void()> Then);
	void SetAllocation(const FString& Json);
	static bool ParseAllocation(const FString& Json, FSurvivalServersAllocation& Out);
	void ReportPlayers();
	void EndMatch(const FString& Reason, bool bSelfStop);
	void WriteResult();
	static FString PlayerKey(AController* Controller);

	TSharedPtr<FState> State;
	FSurvivalServersAllocation Allocation;
	FString EndReason;
	double StartedAt = 0;
	double FrameWindowStart = 0;
	bool bReady = false;
	bool bAllocated = false;
	bool bEnded = false;
	bool bReporting = false;
	bool bReportAgain = false;
	int32 TickHz = 0;
	TFunction<void()> AfterReports;
	FDelegateHandle MapLoadedHandle;
	FDelegateHandle LoginHandle;
	FDelegateHandle LogoutHandle;
	FTSTicker::FDelegateHandle FrameHandle;
};
