extends Node
## A dedicated game server's side of the platform, as the autoload GameServer.
##
## Turning the Survival Servers Studio add-on on (Project > Project Settings > Plugins) adds this autoload, so every scene can call
## GameServer. When the platform starts your server it talks to the local API in GS_LOCAL_API_URL; run anywhere else
## (the editor, your own PC) every call does nothing, so the same build also runs as a plain local server.
##
## The usual order, once your game port is bound:
##
##     GameServer.allocated.connect(_on_match)
##     GameServer.shutdown_requested.connect(_on_stop_notice)
##     await GameServer.ready_for_players()
##
## ready_for_players() reads a fresh start's match before saying ready, so allocated fires before the first player can
## arrive. A ready server (pre-started) gets allocated later, when a start takes it. Then report players
## with player_connect() and player_disconnect(), and end the match with stop() and quit, or save and quit before the
## deadline when shutdown_requested fires.

## A match took this game server. The Dictionary is the allocation: external_id (your match ID), labels, env (the
## start's settings, secret values included: never print them) and args.
signal allocated(allocation: Dictionary)
## The platform asked this game server to stop: save and quit before notice["deadline"]. notice["reason"] says why,
## for example "api", "ttl" or "restart".
signal shutdown_requested(notice: Dictionary)

## ready_for_players() prints this line; a version can use it as its ready check instead of the call.
const READY_LINE := "Server ready for players"
## How long each wait holds its call open, in seconds. The local API allows up to 60.
const LONG_POLL_SECONDS := 30

## True when the platform started this process (GS_LOCAL_API_URL is set) and the calls reach it.
var enabled := false
## This game server's ID (GS_ID), or "local" when run by hand.
var id := "local"
## The match, once allocated; empty before that.
var allocation: Dictionary = {}
## The players reported with player_connect(), in the order they joined.
var players: Array[String] = []

var _agent_url := ""
var _token := ""
var _waiting := false
var _stopping := false
var _sending_players := false
var _players_again := false
var _player_count := -1


func _init() -> void:
	_agent_url = OS.get_environment("GS_LOCAL_API_URL")
	if _agent_url == "":
		_agent_url = OS.get_environment("GS_AGENT_URL")
	_agent_url = _agent_url.trim_suffix("/")
	_token = OS.get_environment("GS_TOKEN")
	enabled = _agent_url != "" and not Engine.is_editor_hint()
	if OS.has_environment("GS_ID"):
		id = OS.get_environment("GS_ID")


## The port to listen on for the version's port `name`: the port inside the container in a Linux image, the public
## port on Windows, and `fallback` when run by hand.
func port(name := "game", fallback := 7777) -> int:
	var variable := "GS_PORT_" + name.to_upper().replace("-", "_")
	for each in [variable + "_INTERNAL", variable]:
		var value := OS.get_environment(each)
		if value.is_valid_int() and int(value) > 0:
			return int(value)
	return fallback


## The address players connect to for port `name`, such as 203.0.113.24:31042; empty when run by hand.
func public_address(name := "game") -> String:
	var ip := OS.get_environment("GS_PUBLIC_IP")
	var public_port := OS.get_environment("GS_PORT_" + name.to_upper().replace("-", "_"))
	if ip == "" or public_port == "":
		return ""
	if ":" in ip:
		return "[%s]:%s" % [ip, public_port]
	return "%s:%s" % [ip, public_port]


## Says this game server is ready for players, then waits in the background for its match (when it has none yet) and
## for a shutdown notice. Call it once, after your game port is bound. Returns false when the platform turned the call
## down; the reason is printed.
func ready_for_players() -> bool:
	if not enabled:
		_read_allocation_file()
		print(READY_LINE)
		return true
	if allocation.is_empty():
		# A fresh start is allocated before it starts: set its match up before saying ready.
		await wait_allocation(0)
	print(READY_LINE)
	if not _ok(await _call(HTTPClient.METHOD_POST, "/v0/self/ready"), "ready"):
		return false
	if not _waiting:
		_waiting = true
		_allocation_loop()
		_shutdown_loop()
	return true


## Waits up to `seconds` (0 to 60) for this game server's match, and returns it: empty if none came in that time.
## allocated fires when it comes. ready_for_players() does this for you.
func wait_allocation(seconds := LONG_POLL_SECONDS) -> Dictionary:
	if not enabled or not allocation.is_empty():
		return allocation
	var reply := await _call(HTTPClient.METHOD_GET, "/v0/self/allocation?wait=%ds" % clampi(seconds, 0, 60))
	if reply[0] == 200:
		_take_allocation(reply[1])
	return allocation


## Waits up to `seconds` (0 to 60) for a shutdown notice, and returns it: empty if none came in that time.
## ready_for_players() does this for you and fires shutdown_requested instead.
func wait_shutdown(seconds := LONG_POLL_SECONDS) -> Dictionary:
	if not enabled:
		return {}
	var reply := await _call(HTTPClient.METHOD_GET, "/v0/self/shutdown?wait=%ds" % clampi(seconds, 0, 60))
	if reply[0] == 200:
		var notice = JSON.parse_string(reply[1])
		if notice is Dictionary:
			return notice
	return {}


## A player joined. The whole list goes out each time, so the count never drifts. Players are also how the platform
## knows a persistent server is empty and can pause it, so report them as they come and go. An ID is 1 to 128
## characters with no control characters; any other (often a name a player chose) is refused (false) and never joins
## the list, so the others keep counting.
func player_connect(player_id: String) -> bool:
	var problem := player_id_problem(player_id)
	if problem != "":
		push_warning("game server: the player ID %s; not reported" % problem)
		return false
	if players.has(player_id):
		return true
	players.append(player_id)
	_player_count = -1
	_send_players()
	return true


## What is wrong with a player ID, or "" when nothing is: the agent's rule, 1 to 128 characters with no control
## characters (it refuses a list holding such an ID whole).
static func player_id_problem(player_id: String) -> String:
	if player_id == "":
		return "is empty"
	if player_id.length() > 128:
		return "is longer than 128 characters"
	for i in player_id.length():
		var c := player_id.unicode_at(i)
		if c < 0x20 or (c >= 0x7f and c <= 0x9f):
			return "has a control character"
	return ""


## A player left.
func player_disconnect(player_id: String) -> void:
	if not players.has(player_id):
		return
	players.erase(player_id)
	_player_count = -1
	_send_players()


## For a game without player IDs: how many are playing now.
func set_player_count(count: int) -> void:
	_player_count = maxi(count, 0)
	_send_players()


## Sets a label on this game server (names: up to 63 of a-z 0-9 _ . -), for finding it through the API.
func set_label(label: String, value: String) -> bool:
	if not enabled:
		return true
	return _ok(await _call(HTTPClient.METHOD_PATCH, "/v0/self/labels", {label: value}), "set label")


## Removes a label.
func remove_label(label: String) -> bool:
	if not enabled:
		return true
	return _ok(await _call(HTTPClient.METHOD_PATCH, "/v0/self/labels", {label: null}), "remove label")


## Reports the last second's update times in milliseconds (median and 99th percentile) and, if you like, the target
## rate in updates a second. They are shown beside the CPU and memory graphs.
func tick(p50_ms: float, p99_ms: float, hz := 0) -> bool:
	if not enabled:
		return true
	var body := {"p50_ms": p50_ms, "p99_ms": p99_ms}
	if hz > 0:
		body["hz"] = hz
	return _ok(await _call(HTTPClient.METHOD_POST, "/v0/self/tick", body), "tick")


## Says this game server is alive, for a version whose health check is the local API.
func health() -> bool:
	if not enabled:
		return true
	return _ok(await _call(HTTPClient.METHOD_POST, "/v0/self/health"), "health")


## Ends this game server: the match is over. The last player list goes out first. Quit afterwards; quitting with
## code 0 counts the same as this call, so a crash is never mistaken for the end of a match.
func stop(reason := "") -> bool:
	_stopping = true
	if not enabled:
		return true
	while _sending_players:
		await get_tree().process_frame
	var body := {}
	if reason != "":
		body["reason"] = reason
	return _ok(await _call(HTTPClient.METHOD_POST, "/v0/self/stop", body), "stop")


## A persistent server just finished a save: it is backed up at once, and with a name, kept under that name. Optional:
## saves are backed up without it. An agent older than this call answers 404, which is not an error. Too soon (once every
## 10 seconds, with a name once a minute) prints the reason and answers false.
func saved(keep := "") -> bool:
	if not enabled:
		return true
	var body := {}
	if keep != "":
		body["keep"] = keep
	var reply := await _call(HTTPClient.METHOD_POST, "/v0/self/saved", body)
	return reply[0] == 404 or _ok(reply, "saved")


func _allocation_loop() -> void:
	while allocation.is_empty() and not _stopping:
		var reply := await _call(HTTPClient.METHOD_GET, "/v0/self/allocation?wait=%ds" % LONG_POLL_SECONDS)
		if reply[0] == 200:
			_take_allocation(reply[1])
		elif reply[0] != 204:
			await get_tree().create_timer(1.0).timeout


func _shutdown_loop() -> void:
	while not _stopping:
		var reply := await _call(HTTPClient.METHOD_GET, "/v0/self/shutdown?wait=%ds" % LONG_POLL_SECONDS)
		if _stopping:
			return
		if reply[0] == 200:
			var notice = JSON.parse_string(reply[1])
			_stopping = true
			shutdown_requested.emit(notice if notice is Dictionary else {})
			return
		if reply[0] != 204:
			await get_tree().create_timer(1.0).timeout


func _take_allocation(body: String) -> void:
	var data = JSON.parse_string(body)
	if data is Dictionary and allocation.is_empty():
		allocation = data
		allocated.emit(data)


## Without a local API: the allocation file, the same data as the allocation call without secret values.
func _read_allocation_file() -> void:
	var path := OS.get_environment("GS_ALLOCATION_FILE")
	if path != "" and FileAccess.file_exists(path):
		_take_allocation(FileAccess.get_file_as_string(path))


## Sends the newest player list (or count), one call at a time: a change during a call is sent after it.
func _send_players() -> void:
	if not enabled:
		return
	if _sending_players:
		_players_again = true
		return
	_sending_players = true
	while true:
		_players_again = false
		var body := {"count": _player_count} if _player_count >= 0 else {"ids": players.duplicate()}
		_ok(await _call(HTTPClient.METHOD_PUT, "/v0/self/players", body), "players")
		if not _players_again:
			break
	_sending_players = false


func _ok(reply: Array, what: String) -> bool:
	if reply[0] >= 200 and reply[0] < 300:
		return true
	printerr("GameServer %s: %s" % [what, "no answer" if reply[0] == 0 else "%d %s" % [reply[0], reply[1]]])
	return false


## One call to the local API. Returns [status, body]; the status is 0 when the call did not complete.
func _call(method: int, path: String, body = null) -> Array:
	var request := HTTPRequest.new()
	request.timeout = 70.0  # longer than the longest wait
	add_child(request)
	var headers := PackedStringArray(["Authorization: Bearer " + _token])
	var data := ""
	if body != null:
		headers.append("Content-Type: application/json")
		data = JSON.stringify(body)
	if request.request(_agent_url + path, headers, method, data) != OK:
		request.queue_free()
		return [0, ""]
	var done: Array = await request.request_completed
	request.queue_free()
	if done[0] != HTTPRequest.RESULT_SUCCESS:
		return [0, ""]
	return [done[1], (done[3] as PackedByteArray).get_string_from_utf8()]
