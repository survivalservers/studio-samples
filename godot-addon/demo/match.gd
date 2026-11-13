extends Node
## The demo: a tiny match server that uses the GameServer autoload for everything the platform offers.
##
## The "game" is a UDP protocol: "join <name>", "leave <name>", "ping". The match ends when everyone who joined has
## left; a shutdown notice ends it early. Run by hand it is a plain local server on port 7777.

var udp := PacketPeerUDP.new()
var joined := false
var peak := 0
var ending := false
var started_ms := 0
var update_ms: Array[float] = []


func _ready() -> void:
	started_ms = Time.get_ticks_msec()
	var err := udp.bind(GameServer.port())
	if err != OK:
		printerr("bind game port %d: %s" % [GameServer.port(), error_string(err)])
		get_tree().quit(1)
		return
	var address := GameServer.public_address()
	print("listening on udp %d%s" % [GameServer.port(), "; players connect to " + address if address != "" else ""])
	GameServer.allocated.connect(_on_allocated)
	GameServer.shutdown_requested.connect(_on_shutdown_requested)
	if not await GameServer.ready_for_players():
		get_tree().quit(1)
		return
	# Once a second: how long the updates took, shown beside the CPU and memory graphs.
	var timer := Timer.new()
	timer.wait_time = 1.0
	timer.timeout.connect(_report_update_times)
	add_child(timer)
	timer.start()


func _on_allocated(allocation: Dictionary) -> void:
	# Setting names only: values can be secret, and the console is a log kept for 90 days.
	print("allocated: match \"%s\", labels %s, settings %s" % [allocation.get("external_id", ""), allocation.get("labels", {}), allocation.get("env", {}).keys()])
	if GameServer.enabled:
		GameServer.set_label("mode", str(allocation.get("env", {}).get("MATCH_MODE", "casual")).to_lower())


func _on_shutdown_requested(notice: Dictionary) -> void:
	print("stop asked (%s); saving and leaving" % notice.get("reason", "api"))
	_finish("stop_asked", false)


func _process(_delta: float) -> void:
	var begin := Time.get_ticks_usec()
	while not ending and udp.get_available_packet_count() > 0:
		var line := udp.get_packet().get_string_from_utf8().strip_edges()
		var reply := _handle(line)
		udp.set_dest_address(udp.get_packet_ip(), udp.get_packet_port())
		udp.put_packet(reply.to_utf8_buffer())
	update_ms.append((Time.get_ticks_usec() - begin) / 1000.0)


func _report_update_times() -> void:
	if update_ms.is_empty() or ending:
		return
	update_ms.sort()
	var p50 := update_ms[update_ms.size() / 2]
	var p99 := update_ms[mini(update_ms.size() - 1, int(update_ms.size() * 0.99))]
	update_ms.clear()
	GameServer.tick(p50, p99, Engine.physics_ticks_per_second)


func _handle(line: String) -> String:
	var parts := line.split(" ", false, 1)
	var cmd := parts[0] if parts.size() > 0 else ""
	var who := parts[1].strip_edges() if parts.size() > 1 else ""
	if cmd == "ping":
		return "pong"
	if cmd == "join" and who != "":
		if GameServer.players.has(who):
			return "already in " + GameServer.id
		if not GameServer.player_connect(who):
			return "refused: a name is 1 to 128 characters with no control characters"
		joined = true
		peak = max(peak, GameServer.players.size())
		print("%s joined (%d playing)" % [who, GameServer.players.size()])
		return "welcome %s to %s" % [who, GameServer.id]
	if cmd == "leave" and who != "":
		if not GameServer.players.has(who):
			return "not in " + GameServer.id
		GameServer.player_disconnect(who)
		print("%s left (%d playing)" % [who, GameServer.players.size()])
		if joined and GameServer.players.is_empty():
			_finish("match_over", true)
		return "bye " + who
	return "unknown command; try join <name>, leave <name> or ping"


## Writes the result file, tells the platform the match is over when the game decided it, and quits with code 0.
func _finish(reason: String, self_stop: bool) -> void:
	if ending:
		return
	ending = true
	var seconds := (Time.get_ticks_msec() - started_ms) / 1000
	print("match over: %s after %ds, %d players at most" % [reason, seconds, peak])
	_write_result(reason, seconds)
	if self_stop:
		await GameServer.stop(reason)
	get_tree().quit(0)


## match-result.json in GS_ARTIFACT_DIR is collected after the game server exits.
func _write_result(reason: String, seconds: int) -> void:
	var dir := OS.get_environment("GS_ARTIFACT_DIR")
	if dir == "":
		return
	var result := {"game_server_id": GameServer.id, "reason": reason, "peak_players": peak, "seconds": seconds}
	if not GameServer.allocation.is_empty():
		result["match"] = GameServer.allocation.get("external_id", "")
	var file := FileAccess.open(dir.path_join("match-result.json"), FileAccess.WRITE)
	if file:
		file.store_string(JSON.stringify(result, "  ") + "\n")
