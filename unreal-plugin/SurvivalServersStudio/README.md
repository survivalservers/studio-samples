# Survival Servers Studio plugin for Unreal Engine

Runs your Unreal dedicated server on Survival Servers Studio. You don't need it: a server started with
`-Port={{port.game}}` runs as it is. The plugin adds the optional calls, with no code of yours:

- says the server is ready once its map is loaded and it listens, so no player is sent to a port nobody listens on;
- reads the match (its ID, settings, labels and arguments) before the first player arrives;
- reports players from the game mode's logins and logouts, sending the whole list after each change;
- sends the server's frame times once a second for the tick graph;
- hears the shutdown notice, tells your game, then ends the match and exits;
- writes `match-result.json` for the platform to collect.

Every call does nothing when the server is not hosted (a run on your own PC). It works only in dedicated servers;
clients and the editor never create it. Built and tested with Unreal Engine 5.8, for Windows and Linux servers.
Apache-2.0.

## Install

1. Copy the `SurvivalServersStudio` folder into your project's `Plugins` folder.
2. Enable it in the project file, or in Edit > Plugins:

   ```json
   "Plugins": [{ "Name": "SurvivalServersStudio", "Enabled": true }]
   ```

3. To call it from C++, add `"SurvivalServersStudio"` to your module's dependencies in its `.Build.cs`.
4. In your version's settings, use the `local_api` ready check: `"ready_check": { "type": "local_api" }`.

## Settings

Project Settings > Plugins > Survival Servers Studio, saved in `Config/DefaultGame.ini`:

| Setting | Default | What it does |
|---|---|---|
| Ready When Map Loaded | on | Says ready once the map is loaded. Turn off to call Ready yourself. |
| Report Players From Logins | on | Reports players from logins and logouts. Turn off to report them yourself. |
| Stop When Last Player Leaves | off | Ends a match once everyone who joined has left. Never on a persistent server. |
| Exit On Stop Request | on | After On Stop Requested, ends the match and exits. Turn off to exit yourself, by halfway through the version's stop grace: the stop signal comes then if the server still runs. |
| Report Frame Times | on | Sends the work time per frame (the frame minus the time it slept) once a second. |

```ini
[/Script/SurvivalServersStudio.SurvivalServersSettings]
bStopWhenLastPlayerLeaves=True
```

## Calls

`USurvivalServersSubsystem` is a game-instance subsystem. In Blueprints, every call is under Survival Servers.

```cpp
#include "SurvivalServersSubsystem.h"

USurvivalServersSubsystem* Server = GetGameInstance()->GetSubsystem<USurvivalServersSubsystem>();
if (Server)  // null on clients and in the editor
{
    Server->SetLabel(TEXT("map"), TEXT("desert"));
    Server->OnStopRequested.AddDynamic(this, &AMyGameMode::SaveBeforeStop);
}
```

| Call | What it does |
|---|---|
| `Ready` | Ready for players, for the `local_api` ready check |
| `WaitAllocation(WaitSeconds, Done)` | C++: waits up to 60 s for the match |
| `WaitShutdown(WaitSeconds, Done)` | C++: waits up to 60 s for a shutdown notice |
| `PlayerConnect(Id)`, `PlayerDisconnect(Id)` | A player joined or left |
| `SetPlayerCount(Count)` | The player count, for games without player IDs |
| `SetLabel(Name, Value)`, `RemoveLabel(Name)` | A label your backend filters on |
| `Tick(P50Ms, P99Ms, Hz)` | Frame times for the last second (Report Frame Times in Blueprints) |
| `Health()` | Still healthy, for a version's `health_timeout` |
| `Saved(Keep)` | A persistent server just saved: back it up now, and keep it under that name |
| `Stop(Reason)` | Ends the match: the last player report, the result file, the self-stop, then the exit |
| `GetMatchId`, `GetSetting(Name, Value)` | The match ID and one of its settings, secret values included |
| `GetGameServerId`, `GetPublicIp`, `GetPublicPort` | The game server and the address players connect to |
| `IsHosted`, `IsPersistentServer` | Whether it runs on the platform, and as a persistent server |
| `OnMatchAllocated(MatchId)` | Event: the match arrived |
| `OnStopRequested(Reason, Deadline)` | Event: save now; the server exits after it unless the settings say otherwise |

The calls have the same names as the platform's SDK for other engines, and go to the game server's
[local API](https://studio.survivalservers.com/docs/game-server/local-api/) through Unreal's own HTTP module. Player IDs are the
players' unique net IDs, or their names when there is none. A name a player chose that the platform cannot take (a
control character, more than 128 characters) is left out of the list with a warning in the log.

## Packaging the server

Dedicated server targets need Unreal built from source, and a Shipping server writes no log unless its target sets
`bUseLoggingInShipping`. The [Unreal guide](https://studio.survivalservers.com/docs/get-started/unreal/) has the target, the
packaging line and the version settings for Windows and Linux.
