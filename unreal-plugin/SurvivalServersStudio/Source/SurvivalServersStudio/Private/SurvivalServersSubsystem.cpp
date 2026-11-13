#include "SurvivalServersSubsystem.h"

#include "Core/GameServerCore.h"
#include "Dom/JsonObject.h"
#include "Engine/NetDriver.h"
#include "Engine/World.h"
#include "GameFramework/Controller.h"
#include "GameFramework/GameModeBase.h"
#include "GameFramework/PlayerState.h"
#include "HAL/PlatformMisc.h"
#include "HAL/PlatformTime.h"
#include "HttpModule.h"
#include "Interfaces/IHttpRequest.h"
#include "Interfaces/IHttpResponse.h"
#include "Misc/App.h"
#include "Misc/FileHelper.h"
#include "Misc/Paths.h"
#include "Serialization/JsonReader.h"
#include "Serialization/JsonSerializer.h"
#include "SurvivalServersSettings.h"
#include "UObject/UObjectGlobals.h"

DEFINE_LOG_CATEGORY(LogSurvivalServers);

namespace Core = SurvivalServersCore;

// The plain C++ rules, kept out of the public header.
struct USurvivalServersSubsystem::FState
{
	Core::Config Config;
	Core::Match Match;
	Core::FrameTimes Frames;
};

namespace
{
	FString ToF(const std::string& S) { return FString(UTF8_TO_TCHAR(S.c_str())); }
	std::string ToS(const FString& S) { return std::string(TCHAR_TO_UTF8(*S)); }
	constexpr int32 LongPollSeconds = 30;
	const USurvivalServersSettings& Settings() { return *GetDefault<USurvivalServersSettings>(); }
}

bool USurvivalServersSubsystem::ShouldCreateSubsystem(UObject* Outer) const
{
	return IsRunningDedicatedServer();
}

void USurvivalServersSubsystem::Initialize(FSubsystemCollectionBase& Collection)
{
	Super::Initialize(Collection);
	State = MakeShared<FState>();
	std::string Error;
	State->Config = Core::Config::FromEnv([](const char* Name) { return ToS(FPlatformMisc::GetEnvironmentVariable(UTF8_TO_TCHAR(Name))); }, Error);
	if (!Error.empty())
	{
		UE_LOG(LogSurvivalServers, Error, TEXT("%s"), *ToF(Error));
		FPlatformMisc::RequestExitWithStatus(false, 1);
		return;
	}
	const Core::Config& Config = State->Config;
	StartedAt = FPlatformTime::Seconds();
	UE_LOG(LogSurvivalServers, Display, TEXT("game server %s; players connect to %s:%d"), *ToF(Config.Id), *ToF(Config.PublicIp), Config.PublicPort);

	// Without a local API (a run on your own PC), the match data comes from the allocation file, if any.
	FString FileJson;
	if (!Config.HasAgent() && !Config.AllocationFile.empty() && FFileHelper::LoadFileToString(FileJson, *ToF(Config.AllocationFile)))
	{
		SetAllocation(FileJson);
	}

	MapLoadedHandle = FCoreUObjectDelegates::PostLoadMapWithWorld.AddUObject(this, &USurvivalServersSubsystem::OnMapLoaded);
	if (Settings().bReportPlayersFromLogins)
	{
		LoginHandle = FGameModeEvents::GameModePostLoginEvent.AddUObject(this, &USurvivalServersSubsystem::OnPostLogin);
		LogoutHandle = FGameModeEvents::GameModeLogoutEvent.AddUObject(this, &USurvivalServersSubsystem::OnLogout);
	}
	if (Settings().bReportFrameTimes && Config.HasAgent())
	{
		FrameWindowStart = FPlatformTime::Seconds();
		FrameHandle = FTSTicker::GetCoreTicker().AddTicker(FTickerDelegate::CreateUObject(this, &USurvivalServersSubsystem::OnFrame));
	}
}

void USurvivalServersSubsystem::Deinitialize()
{
	// The engine is shutting down (the platform's stop signal, or the exit after a self-stop): write what we know.
	if (State && !bEnded)
	{
		bEnded = true;
		EndReason = TEXT("stop_signal");
		WriteResult();
	}
	FCoreUObjectDelegates::PostLoadMapWithWorld.Remove(MapLoadedHandle);
	FGameModeEvents::GameModePostLoginEvent.Remove(LoginHandle);
	FGameModeEvents::GameModeLogoutEvent.Remove(LogoutHandle);
	if (FrameHandle.IsValid()) FTSTicker::GetCoreTicker().RemoveTicker(FrameHandle);
	Super::Deinitialize();
}

// The server listens once its map is loaded: say ready then, so no player is sent to a port nobody listens on.
void USurvivalServersSubsystem::OnMapLoaded(UWorld* World)
{
	if (bReady || !World || World->GetNetMode() != NM_DedicatedServer) return;
	bReady = true;
	if (const UNetDriver* Driver = World->GetNetDriver()) TickHz = Driver->GetNetServerMaxTickRate();
	// A version can use this exact line as its ready check instead of the call below.
	UE_LOG(LogSurvivalServers, Display, TEXT("Server ready for players"));
	if (!State->Config.HasAgent()) return;

	// A fresh start is allocated before it starts: read the match now, before saying ready, so it is set up before
	// the first player arrives. A ready server (pre-started) has none yet (204) and waits for it after ready.
	Call(TEXT("GET"), ToF(State->Config.AgentUrl) + TEXT("/v0/self/allocation"), FString(), [this](int32 Status, const FString& Body)
	{
		if (Status == 200) SetAllocation(Body);
		TFunction<void()> Watch = [this]
		{
			// The allocation call is the only place secret settings come from.
			if (!bAllocated) PollAllocation();
			PollShutdown();
		};
		if (!Settings().bReadyWhenMapLoaded)
		{
			Watch();
			return;
		}
		Ready([this, Watch](bool bOk)
		{
			if (!bOk)
			{
				FPlatformMisc::RequestExitWithStatus(false, 1);
				return;
			}
			Watch();
		});
	});
}

void USurvivalServersSubsystem::Ready(TFunction<void(bool)> Done)
{
	if (!IsHosted())
	{
		if (Done) Done(true);
		return;
	}
	Call(TEXT("POST"), ToF(State->Config.AgentUrl) + TEXT("/v0/self/ready"), FString(), [Done](int32 Status, const FString& Body)
	{
		const bool bOk = Status >= 200 && Status < 300;
		if (!bOk) UE_LOG(LogSurvivalServers, Error, TEXT("ready call failed: %d %s"), Status, *Body);
		if (Done) Done(bOk);
	});
}

void USurvivalServersSubsystem::Health()
{
	Send(TEXT("POST"), TEXT("/v0/self/health"), FString());
}

void USurvivalServersSubsystem::Stop(const FString& Reason)
{
	EndMatch(Reason, true);
}

void USurvivalServersSubsystem::WaitAllocation(int32 WaitSeconds, TFunction<void(const FSurvivalServersAllocation*)> Done)
{
	if (!IsHosted())
	{
		Done(nullptr);
		return;
	}
	Call(TEXT("GET"), ToF(Core::LongPollUrl(State->Config, "allocation", WaitSeconds)), FString(), [Done](int32 Status, const FString& Body)
	{
		FSurvivalServersAllocation Got;
		Done(Status == 200 && ParseAllocation(Body, Got) ? &Got : nullptr);
	});
}

void USurvivalServersSubsystem::WaitShutdown(int32 WaitSeconds, TFunction<void(const FSurvivalServersShutdown*)> Done)
{
	if (!IsHosted())
	{
		Done(nullptr);
		return;
	}
	Call(TEXT("GET"), ToF(Core::LongPollUrl(State->Config, "shutdown", WaitSeconds)), FString(), [Done](int32 Status, const FString& Body)
	{
		TSharedPtr<FJsonObject> Object;
		FSurvivalServersShutdown Shutdown;
		FString Deadline;
		if (Status != 200 || !FJsonSerializer::Deserialize(TJsonReaderFactory<>::Create(Body), Object) || !Object.IsValid())
		{
			Done(nullptr);
			return;
		}
		Object->TryGetStringField(TEXT("reason"), Shutdown.Reason);
		if (Object->TryGetStringField(TEXT("deadline"), Deadline)) FDateTime::ParseIso8601(*Deadline, Shutdown.Deadline);
		Done(&Shutdown);
	});
}

void USurvivalServersSubsystem::SetPlayerCount(int32 Count)
{
	Send(TEXT("PUT"), TEXT("/v0/self/players"), ToF(Core::CountBody(Count)));
}

void USurvivalServersSubsystem::SetLabel(const FString& Name, const FString& Value)
{
	const std::string V = ToS(Value);
	Send(TEXT("PATCH"), TEXT("/v0/self/labels"), ToF(Core::LabelBody(ToS(Name), &V)));
}

void USurvivalServersSubsystem::RemoveLabel(const FString& Name)
{
	Send(TEXT("PATCH"), TEXT("/v0/self/labels"), ToF(Core::LabelBody(ToS(Name), nullptr)));
}

void USurvivalServersSubsystem::Tick(double P50Ms, double P99Ms, int32 Hz)
{
	Send(TEXT("POST"), TEXT("/v0/self/tick"), ToF(Core::TickBody(P50Ms, P99Ms, Hz)));
}

void USurvivalServersSubsystem::Saved(const FString& Keep)
{
	if (!IsHosted()) return;
	Call(TEXT("POST"), ToF(State->Config.AgentUrl) + TEXT("/v0/self/saved"), ToF(Core::SavedBody(ToS(Keep))), [](int32 Status, const FString& Reply)
	{
		if (Status != 404 && (Status < 200 || Status >= 300)) UE_LOG(LogSurvivalServers, Warning, TEXT("saved: %d %s"), Status, *Reply);
	});
}

bool USurvivalServersSubsystem::IsHosted() const
{
	return State && State->Config.HasAgent();
}

bool USurvivalServersSubsystem::IsPersistentServer() const
{
	return State && State->Config.IsPersistent();
}

FString USurvivalServersSubsystem::GetGameServerId() const
{
	return State ? ToF(State->Config.Id) : FString();
}

FString USurvivalServersSubsystem::GetPublicIp() const
{
	return State ? ToF(State->Config.PublicIp) : FString();
}

int32 USurvivalServersSubsystem::GetPublicPort() const
{
	return State ? State->Config.PublicPort : 0;
}

bool USurvivalServersSubsystem::GetSetting(const FString& Name, FString& Value) const
{
	const FString* Found = Allocation.Env.Find(Name);
	Value = Found ? *Found : FString();
	return Found != nullptr;
}

int32 USurvivalServersSubsystem::GetPlayerCount() const
{
	return State ? State->Match.Count() : 0;
}

// Each engine frame: its work time (the frame minus the time the engine slept to hold its tick rate). Once a second
// the median and the 99th percentile go to the tick graph.
bool USurvivalServersSubsystem::OnFrame(float DeltaSeconds)
{
	if (bEnded) return true;
	State->Frames.Add((double(DeltaSeconds) - FApp::GetIdleTime()) * 1000.0);
	const double Now = FPlatformTime::Seconds();
	if (Now - FrameWindowStart < 1.0) return true;
	FrameWindowStart = Now;
	if (!bReady) return true;
	double P50 = 0, P99 = 0;
	State->Frames.Take(P50, P99);
	Tick(P50, P99, TickHz);
	return true;
}

// A call whose answer only matters when it fails.
void USurvivalServersSubsystem::Send(const FString& Verb, const FString& Path, const FString& Body)
{
	if (!IsHosted()) return;
	Call(Verb, ToF(State->Config.AgentUrl) + Path, Body, [Verb, Path](int32 Status, const FString& Reply)
	{
		if (Status < 200 || Status >= 300) UE_LOG(LogSurvivalServers, Warning, TEXT("%s %s: %d %s"), *Verb, *Path, Status, *Reply);
	});
}

void USurvivalServersSubsystem::Call(const FString& Verb, const FString& Url, const FString& Body, TFunction<void(int32, const FString&)> Done)
{
	TSharedRef<IHttpRequest, ESPMode::ThreadSafe> Request = FHttpModule::Get().CreateRequest();
	Request->SetURL(Url);
	Request->SetVerb(Verb);
	Request->SetHeader(TEXT("Authorization"), TEXT("Bearer ") + ToF(State->Config.Token));
	if (!Body.IsEmpty())
	{
		Request->SetHeader(TEXT("Content-Type"), TEXT("application/json"));
		Request->SetContentAsString(Body);
	}
	Request->SetTimeout(Core::MaxWaitSeconds + 10.0f);
	TWeakObjectPtr<USurvivalServersSubsystem> Self(this);
	Request->OnProcessRequestComplete().BindLambda([Self, Done](FHttpRequestPtr, FHttpResponsePtr Response, bool bConnected)
	{
		if (!Self.IsValid()) return;
		const bool bAnswered = bConnected && Response.IsValid();
		Done(bAnswered ? Response->GetResponseCode() : 0, bAnswered ? Response->GetContentAsString() : FString());
	});
	Request->ProcessRequest();
}

void USurvivalServersSubsystem::After(float Seconds, TFunction<void()> Then)
{
	FTSTicker::GetCoreTicker().AddTicker(FTickerDelegate::CreateWeakLambda(this, [Then](float) { Then(); return false; }), Seconds);
}

void USurvivalServersSubsystem::PollAllocation()
{
	if (bEnded) return;
	Call(TEXT("GET"), ToF(Core::LongPollUrl(State->Config, "allocation", LongPollSeconds)), FString(), [this](int32 Status, const FString& Body)
	{
		switch (Core::AfterLongPoll(Status))
		{
		case Core::PollNext::Use: SetAllocation(Body); break;
		case Core::PollNext::Again: PollAllocation(); break;
		case Core::PollNext::Pause: After(1.0f, [this] { PollAllocation(); }); break;
		}
	});
}

void USurvivalServersSubsystem::PollShutdown()
{
	if (bEnded) return;
	Call(TEXT("GET"), ToF(Core::LongPollUrl(State->Config, "shutdown", LongPollSeconds)), FString(), [this](int32 Status, const FString& Body)
	{
		// The match already ended here (its own stop, whose notice this wait then hears): nothing to tell the game.
		if (bEnded) return;
		switch (Core::AfterLongPoll(Status))
		{
		case Core::PollNext::Use:
		{
			TSharedPtr<FJsonObject> Object;
			FString Reason, Deadline;
			FDateTime When = FDateTime::UtcNow();
			if (FJsonSerializer::Deserialize(TJsonReaderFactory<>::Create(Body), Object) && Object.IsValid())
			{
				Object->TryGetStringField(TEXT("reason"), Reason);
				if (Object->TryGetStringField(TEXT("deadline"), Deadline)) FDateTime::ParseIso8601(*Deadline, When);
			}
			UE_LOG(LogSurvivalServers, Display, TEXT("stop asked (%s); saving and leaving"), *Reason);
			OnStopRequested.Broadcast(Reason, When);
			if (Settings().bExitOnStopRequest) EndMatch(TEXT("stop_asked"), false);
			break;
		}
		case Core::PollNext::Again: PollShutdown(); break;
		case Core::PollNext::Pause: After(1.0f, [this] { PollShutdown(); }); break;
		}
	});
}

void USurvivalServersSubsystem::SetAllocation(const FString& Json)
{
	FSurvivalServersAllocation Got;
	if (!ParseAllocation(Json, Got))
	{
		UE_LOG(LogSurvivalServers, Warning, TEXT("allocation data is not JSON"));
		return;
	}
	Allocation = MoveTemp(Got);
	bAllocated = true;
	UE_LOG(LogSurvivalServers, Display, TEXT("allocated: match \"%s\""), *Allocation.ExternalId);
	OnMatchAllocated.Broadcast(Allocation.ExternalId);
}

bool USurvivalServersSubsystem::ParseAllocation(const FString& Json, FSurvivalServersAllocation& Out)
{
	TSharedPtr<FJsonObject> Object;
	if (!FJsonSerializer::Deserialize(TJsonReaderFactory<>::Create(Json), Object) || !Object.IsValid()) return false;
	Object->TryGetStringField(TEXT("game_server_id"), Out.GameServerId);
	Object->TryGetStringField(TEXT("external_id"), Out.ExternalId);
	for (const TCHAR* Field : {TEXT("env"), TEXT("labels")})
	{
		const TSharedPtr<FJsonObject>* Map = nullptr;
		if (!Object->TryGetObjectField(Field, Map) || !Map) continue;
		TMap<FString, FString>& Target = FCString::Strcmp(Field, TEXT("env")) == 0 ? Out.Env : Out.Labels;
		for (const auto& Pair : (*Map)->Values)
		{
			FString Value;
			// The key is an FString or, from Unreal 5.8, a shared string; both give their characters with *.
			if (Pair.Value.IsValid() && Pair.Value->TryGetString(Value)) Target.Add(FString(*Pair.Key), Value);
		}
	}
	const TArray<TSharedPtr<FJsonValue>>* Args = nullptr;
	if (Object->TryGetArrayField(TEXT("args"), Args) && Args)
	{
		for (const TSharedPtr<FJsonValue>& Arg : *Args)
		{
			FString Value;
			if (Arg.IsValid() && Arg->TryGetString(Value)) Out.Args.Add(Value);
		}
	}
	return true;
}

FString USurvivalServersSubsystem::PlayerKey(AController* Controller)
{
	const APlayerState* PlayerState = Controller ? Controller->GetPlayerState<APlayerState>() : nullptr;
	if (!PlayerState) return FString();
	const FString Id = PlayerState->GetUniqueId().ToString();
	return Id.IsEmpty() ? PlayerState->GetPlayerName() : Id;
}

void USurvivalServersSubsystem::OnPostLogin(AGameModeBase*, APlayerController* NewPlayer)
{
	PlayerConnect(PlayerKey(NewPlayer));
}

void USurvivalServersSubsystem::OnLogout(AGameModeBase*, AController* Exiting)
{
	PlayerDisconnect(PlayerKey(Exiting));
}

void USurvivalServersSubsystem::PlayerConnect(const FString& Id)
{
	if (!State || bEnded || Id.IsEmpty()) return;
	// A name a player chose can be the ID: one the agent would refuse (it refuses the whole list) never joins.
	const std::string Problem = Core::PlayerIdProblem(ToS(Id));
	if (!Problem.empty())
	{
		UE_LOG(LogSurvivalServers, Warning, TEXT("a player ID %s; not reported"), *ToF(Problem));
		return;
	}
	if (!State->Match.Join(ToS(Id))) return;
	UE_LOG(LogSurvivalServers, Display, TEXT("%s joined (%d playing)"), *Id, State->Match.Count());
	ReportPlayers();
}

void USurvivalServersSubsystem::PlayerDisconnect(const FString& Id)
{
	if (!State || bEnded || Id.IsEmpty() || !State->Match.Leave(ToS(Id))) return;
	UE_LOG(LogSurvivalServers, Display, TEXT("%s left (%d playing)"), *Id, State->Match.Count());
	ReportPlayers();
	if (State->Match.Over() && Settings().bStopWhenLastPlayerLeaves && !State->Config.IsPersistent()) EndMatch(TEXT("match_over"), true);
}

// One report at a time; a change during a call sends the newest list after it.
void USurvivalServersSubsystem::ReportPlayers()
{
	if (!IsHosted()) return;
	if (bReporting)
	{
		bReportAgain = true;
		return;
	}
	bReporting = true;
	Call(TEXT("PUT"), ToF(State->Config.AgentUrl) + TEXT("/v0/self/players"), ToF(Core::PlayersBody(State->Match.Ids())), [this](int32 Status, const FString&)
	{
		if (Status < 200 || Status >= 300) UE_LOG(LogSurvivalServers, Warning, TEXT("report players: %d"), Status);
		bReporting = false;
		if (bReportAgain)
		{
			bReportAgain = false;
			ReportPlayers();
		}
		else if (AfterReports)
		{
			TFunction<void()> Then = MoveTemp(AfterReports);
			AfterReports = nullptr;
			Then();
		}
	});
}

// Ends the match: after the last player report, the result file; then, if the server decided to end, a self-stop
// (the same as exiting with code 0); then exit.
void USurvivalServersSubsystem::EndMatch(const FString& Reason, bool bSelfStop)
{
	if (!State || bEnded) return;
	bEnded = true;
	EndReason = Reason;
	UE_LOG(LogSurvivalServers, Display, TEXT("match over: %s after %ds, %d players at most"), *Reason, int32(FPlatformTime::Seconds() - StartedAt), State->Match.Peak());
	TFunction<void()> Finish = [this, bSelfStop]
	{
		WriteResult();
		if (!bSelfStop || !IsHosted())
		{
			FPlatformMisc::RequestExit(false);
			return;
		}
		Call(TEXT("POST"), ToF(State->Config.AgentUrl) + TEXT("/v0/self/stop"), ToF(Core::StopBody(ToS(EndReason))), [](int32 Status, const FString&)
		{
			if (Status < 200 || Status >= 300) UE_LOG(LogSurvivalServers, Warning, TEXT("stop: %d (exiting with 0 counts as a stop too)"), Status);
			FPlatformMisc::RequestExit(false);
		});
	};
	if (bReporting) AfterReports = MoveTemp(Finish);
	else Finish();
}

// match-result.json in GS_ARTIFACT_DIR is collected after the game server exits.
void USurvivalServersSubsystem::WriteResult()
{
	if (!State || State->Config.ArtifactDir.empty()) return;
	const std::string Json = Core::ResultJson(State->Config.Id, ToS(EndReason), State->Match.Peak(), int32(FPlatformTime::Seconds() - StartedAt), ToS(Allocation.ExternalId));
	FFileHelper::SaveStringToFile(ToF(Json), *FPaths::Combine(ToF(State->Config.ArtifactDir), TEXT("match-result.json")), FFileHelper::EEncodingOptions::ForceUTF8WithoutBOM);
}
