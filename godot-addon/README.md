# Survival Servers Studio add-on for Godot 4

Runs your Godot game as a dedicated server on the platform. It adds one autoload, `GameServer`, that reads the
server's ports and its match, says when it is ready, reports players, labels and update times, hears shutdown notices,
and ends the match. Plain GDScript over Godot's own `HTTPRequest`: no native code, nothing to build. Apache-2.0.

Run anywhere else (the editor, your own PC) every call does nothing, so the same export also runs as a plain local
server.

## Install

1. Copy `addons/game_server` into your project. (The add-on is not on the Asset Library yet.)
2. **Project > Project Settings > Plugins**: turn **Game Server** on. That adds the `GameServer` autoload; turning it
   off removes it again.

## Use

Once your game port is bound:

```gdscript
func _ready() -> void:
	udp.bind(GameServer.port())                       # GS_PORT_GAME (inside a Linux image: the container's port)
	GameServer.allocated.connect(_on_match)           # the match: external_id, labels, env (secrets included), args
	GameServer.shutdown_requested.connect(_on_stop)   # save and quit before notice["deadline"]
	await GameServer.ready_for_players()


func _on_player_joined(id: String) -> void:
	GameServer.player_connect(id)


func _on_player_left(id: String) -> void:
	GameServer.player_disconnect(id)


func _on_match_over() -> void:
	await GameServer.stop("match_over")
	get_tree().quit(0)
```

`ready_for_players()` reads a fresh start's match before it says ready, so `allocated` fires before the first player
can arrive. A ready server (pre-started) gets `allocated` later, when a start takes it.

| Call | What it does |
|---|---|
| `port(name = "game", fallback = 7777)` | The port to listen on for a named port in the version |
| `public_address(name = "game")` | The address players connect to, such as `203.0.113.24:31042` |
| `ready_for_players()` | Says it is ready, then waits in the background for its match and for a shutdown notice |
| `allocated(allocation)` | Signal: a match took this game server |
| `shutdown_requested(notice)` | Signal: save and quit before `notice["deadline"]` |
| `player_connect(id)`, `player_disconnect(id)` | The player list, sent whole each time; a persistent server set to pause when empty pauses once it stays empty. An ID is 1 to 128 characters with no control characters: `player_connect` refuses any other (false) |
| `set_player_count(count)` | For games without player IDs |
| `set_label(name, value)`, `remove_label(name)` | Labels for finding this game server through the API |
| `tick(p50_ms, p99_ms, hz = 0)` | The last second's update times, shown beside the CPU and memory graphs |
| `health()` | Still alive, for a version whose health check is the local API |
| `stop(reason = "")` | The match is over: sends the last player list, then ends this game server. Quit after it |
| `wait_allocation(seconds)`, `wait_shutdown(seconds)` | The waits `ready_for_players()` runs for you, if you want them yourself (at most 60 seconds) |
| `enabled`, `id`, `allocation`, `players` | Whether the platform started it, its ID, its match, and the players reported |

Godot does not catch SIGTERM, so a Linux version stops a Godot server through the local API
(`"stop": {"method": "local_api"}`): connect `shutdown_requested`, save, and quit.

## The demo

`demo/match.gd` is a tiny UDP match server that uses every part of the add-on: `join <name>`, `leave <name>` and
`ping`, labels from the match settings, update times once a second, a result file in `GS_ARTIFACT_DIR`.

```bash
godot --headless --path .                    # a plain local server on port 7777
GODOT=/path/to/godot go test ./              # against a fake local API, and installed into a fresh project
```

The tests run Godot headless (no window): the demo's match on a ready server, a shutdown notice, the demo with no
platform, and the add-on copied into an empty project and turned on in the editor as a person would.
