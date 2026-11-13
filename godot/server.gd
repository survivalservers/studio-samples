extends Node
## A sample game server for Godot 4 with no plugin. Everything the platform offers is plain HTTP, so Godot's own
## HTTPRequest does it all:
##
## - the port and match settings come from GS_* environment variables and the allocation file;
## - it says it is ready on the local API (or a version can just watch for the line it prints);
## - pre-started as a ready server, it waits for its match on a long-poll;
## - it reports its players, ends the match by stopping itself, and saves on the way out when asked to stop;
## - it leaves a result file to be collected after it exits.
##
## The "game" is a tiny UDP protocol: "join <name>", "leave <name>", "ping". The match ends when everyone who
## joined has left. With no GS_* variables it runs as a plain local server on port 7777.

const READY_LINE := "Server ready for players"
const LONG_POLL := "30s"

var agent_url := OS.get_environment("GS_LOCAL_API_URL") if OS.get_environment("GS_LOCAL_API_URL") != "" else OS.get_environment("GS_AGENT_URL")
var token := OS.get_environment("GS_TOKEN")
var gs_id := OS.get_environment("GS_ID") if OS.has_environment("GS_ID") else "local"

var udp := PacketPeerUDP.new()
var players: Dictionary = {}
var peak := 0
var joined := false
var allocation = null
var ending := false
var started_ms := 0
var reporting := false
var report_again := false


func _ready() -> void:
	started_ms = Time.get_ticks_msec()
	var port := _listen_port()
	var err := udp.bind(port)
	if err != OK:
		printerr("bind game port %d: %s" % [port, error_string(err)])
		get_tree().quit(1)
		return
	print("listening on udp %d; players connect to %s:%s" % [port, OS.get_environment("GS_PUBLIC_IP"), OS.get_environment("GS_PORT_GAME")])
	if agent_url == "":
		print(READY_LINE)
		_read_allocation_file()
		return
	# A fresh start is allocated before it starts: read the match now, before saying ready, so it is set up before
	# the first player arrives. A ready server (pre-started) has none yet (204) and waits for it after ready.
	var first: Array = await _call(HTTPClient.METHOD_GET, "/v0/self/allocation?wait=0s")
	if first[0] == 200:
		var data = JSON.parse_string(first[1])
		if data is Dictionary:
			_set_allocation(data)
	# A version can use this exact line as its ready check instead of the call below.
	print(READY_LINE)
	var reply: Array = await _call(HTTPClient.METHOD_POST, "/v0/self/ready")
	if reply[0] < 200 or reply[0] >= 300:
		printerr("say ready: %d %s" % [reply[0], reply[1]])
		get_tree().quit(1)
		return
	# The allocation call answers at once on a fresh start, and is the only place secret settings come from.
	_wait_for_allocation()
	_wait_for_stop()


## Linux images listen on the container port; Windows builds bind the public port itself.
func _listen_port() -> int:
	for name in ["GS_PORT_GAME_INTERNAL", "GS_PORT_GAME"]:
		var value := OS.get_environment(name)
		if value.is_valid_int() and int(value) > 0:
			return int(value)
	return 7777


func _process(_delta: float) -> void:
	while not ending and udp.get_available_packet_count() > 0:
		var line := udp.get_packet().get_string_from_utf8().strip_edges()
		var ip := udp.get_packet_ip()
		var port := udp.get_packet_port()
		var reply := _handle(line)
		if reply != "":
			udp.set_dest_address(ip, port)
			udp.put_packet(reply.to_utf8_buffer())


## Runs one command from a player.
func _handle(line: String) -> String:
	var parts := line.split(" ", false, 1)
	var cmd := parts[0] if parts.size() > 0 else ""
	var who := parts[1].strip_edges() if parts.size() > 1 else ""
	if cmd == "ping":
		return "pong"
	if cmd == "join" and who != "":
		if _bad_name(who):
			# The platform refuses a player list holding such a name (a player ID is 1 to 128 characters with no
			# control characters), so it never joins; the others keep counting.
			return "refused: a name is 1 to 128 characters with no control characters"
		if players.has(who):
			return "already in " + gs_id
		players[who] = true
		joined = true
		peak = max(peak, players.size())
		print("%s joined (%d playing)" % [who, players.size()])
		_report_players()
		return "welcome %s to %s" % [who, gs_id]
	if cmd == "leave" and who != "":
		if not players.has(who):
			return "not in " + gs_id
		players.erase(who)
		print("%s left (%d playing)" % [who, players.size()])
		_report_players()
		if joined and players.is_empty():
			_finish("match_over", true)
		return "bye " + who
	return "unknown command; try join <name>, leave <name> or ping"


## Without a local API, the allocation file (the same data as the allocation call, minus secret values).
func _read_allocation_file() -> void:
	var path := OS.get_environment("GS_ALLOCATION_FILE")
	if path == "" or not FileAccess.file_exists(path):
		return
	var data = JSON.parse_string(FileAccess.get_file_as_string(path))
	if data is Dictionary:
		_set_allocation(data)


func _set_allocation(data: Dictionary) -> void:
	allocation = data
	# Setting names only: values can be secret, and the console is a log kept for 90 days.
	print("allocated: match \"%s\", labels %s, settings %s" % [data.get("external_id", ""), data.get("labels", {}), data.get("env", {}).keys()])


func _wait_for_allocation() -> void:
	while allocation == null and not ending:
		var reply: Array = await _call(HTTPClient.METHOD_GET, "/v0/self/allocation?wait=" + LONG_POLL)
		if reply[0] == 200:
			var data = JSON.parse_string(reply[1])
			if data is Dictionary:
				_set_allocation(data)
		elif reply[0] != 204:
			await get_tree().create_timer(1.0).timeout


func _wait_for_stop() -> void:
	while not ending:
		var reply: Array = await _call(HTTPClient.METHOD_GET, "/v0/self/shutdown?wait=" + LONG_POLL)
		if reply[0] == 200:
			print("stop asked; saving and leaving")
			_finish("stop_asked", false)
			return
		if reply[0] != 204 and not ending:
			await get_tree().create_timer(1.0).timeout


## Sends the player list, one call at a time; a change during a call sends the newest list after it.
func _report_players() -> void:
	if agent_url == "":
		return
	if reporting:
		report_again = true
		return
	reporting = true
	while true:
		report_again = false
		await _call(HTTPClient.METHOD_PUT, "/v0/self/players", {"ids": players.keys()})
		if not report_again:
			break
	reporting = false


## Ends the match: the last player list goes out, the result file is written, and if the game server decided to
## end (rather than being asked to stop) it tells the platform, which is the same as exiting with code 0.
func _finish(reason: String, self_stop: bool) -> void:
	if ending:
		return
	ending = true
	while reporting:
		await get_tree().process_frame
	var seconds := (Time.get_ticks_msec() - started_ms) / 1000
	print("match over: %s after %ds, %d players at most" % [reason, seconds, peak])
	_write_result(reason, seconds)
	if self_stop and agent_url != "":
		await _call(HTTPClient.METHOD_POST, "/v0/self/stop", {"reason": reason})
	get_tree().quit(0)


## match-result.json in GS_ARTIFACT_DIR is collected after the game server exits.
func _write_result(reason: String, seconds: int) -> void:
	var dir := OS.get_environment("GS_ARTIFACT_DIR")
	if dir == "":
		return
	var result := {"game_server_id": gs_id, "reason": reason, "peak_players": peak, "seconds": seconds}
	if allocation != null:
		result["match"] = allocation.get("external_id", "")
	var file := FileAccess.open(dir.path_join("match-result.json"), FileAccess.WRITE)
	if file:
		file.store_string(JSON.stringify(result, "  ") + "\n")


## One call to the local API. Returns [status, body]; status 0 when the call did not complete.
func _call(method: int, path: String, body = null) -> Array:
	var request := HTTPRequest.new()
	request.timeout = 40.0  # longer than a long-poll's wait
	add_child(request)
	var headers := PackedStringArray(["Authorization: Bearer " + token])
	var data := ""
	if body != null:
		headers.append("Content-Type: application/json")
		data = JSON.stringify(body)
	if request.request(agent_url + path, headers, method, data) != OK:
		request.queue_free()
		return [0, ""]
	var done: Array = await request.request_completed
	request.queue_free()
	if done[0] != HTTPRequest.RESULT_SUCCESS:
		return [0, ""]
	return [done[1], (done[3] as PackedByteArray).get_string_from_utf8()]


## Ctrl+C or a window close request (for example a stop signal, where the platform sends one): save and leave.
func _notification(what: int) -> void:
	if what == NOTIFICATION_WM_CLOSE_REQUEST:
		_finish("stop_signal", false)


func _bad_name(who: String) -> bool:
	if who.length() > 128:
		return true
	for i in who.length():
		var c := who.unicode_at(i)
		if c < 0x20 or (c >= 0x7f and c <= 0x9f):
			return true
	return false
