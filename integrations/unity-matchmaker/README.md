# Unity Matchmaker allocator

A Unity Cloud Code module that lets Unity Matchmaker start your game servers on our platform. For each match,
Matchmaker calls the module; it starts a game server and hands Matchmaker the address players join. No change to
your game client, and no code of ours in your game server. Apache-2.0.

## How it works

| Matchmaker calls | The module | Matchmaker gets |
|---|---|---|
| `Matchmaker_AllocateServer` | `POST /v0/game-servers` with your app, the placement for the match, `Idempotency-Key` and `external_id` set to the match ID, and the match's labels | `Created` with the game server's ID (and its address straight away when a ready server took the match) |
| `Matchmaker_PollAllocation` | `GET /v0/game-servers/{id}` | `Allocated` with the IPv4 address and the game port once the game server is `running`; `Pending` while it starts; `Error` if it failed or stopped |

- **One game server per match, always.** The match ID is the Idempotency-Key, so if Unity calls again for the same
  match, the API returns the game server it already started.
- **Retries.** Rate limits, brief outages and a start still in progress are retried with the same key, at most three
  attempts within a few seconds. "No capacity" fails the ticket at once with the reason, so Unity can match again.
- **Match data in the game server.** The game server finds the match ID in `MATCH_ID` and, unless you turn it off,
  the match's teams and players as JSON in `MATCH_PROPERTIES`. Both arrive in the allocation data (and, on a cold
  start, in the environment). Labels `match_id`, `pool` and `queue` let you find the game server in the panel.

## Set up

1. **API key.** In the panel, make a key with the scopes `game_servers:write` and `game_servers:read`, limited to
   this app. In the Unity Dashboard, open Administration, Secrets, and add it as `GAME_SERVER_API_KEY`.
2. **Settings.** Edit `ModuleConfig` in `Project/ModuleConfig.cs`: your app's slug and the API's address
   (`https://studio.survivalservers.com`, already filled in) are the two required values.
   Optional: a version or branch, where each match runs (`PlacementByRegion` maps the Region Unity picked for the
   match to our regions or continents; `PlacementByPool` maps pool names; `DefaultPlacement` covers the rest, such as
   `new PlacementChoice { Continents = { "north-america" } }`), a time limit.
3. **Deploy** with the Unity Gaming Services command line: `ugs deploy GameServerAllocator.sln`.
4. **Queue.** In the queue's hosting settings choose Cloud Code, the module `GameServerAllocator`, the allocate
   function `Matchmaker_AllocateServer` and the poll function `Matchmaker_PollAllocation`.

## Test

```bash
dotnet test Tests                                   # against a fake of the API
PRACTICE_API=http://127.0.0.1:4010 dotnet test Tests   # also against the practice server
```

Needs the .NET 8 SDK; the module targets .NET 8, which Cloud Code runs.
