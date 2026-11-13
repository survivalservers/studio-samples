extends Node2D
## One project, two roles. Run with --headless, or exported as a dedicated server, it is the server. Otherwise it is
## the client. The server owns the game: a client says where it wants to go, and the server moves it and tells everyone.
##
## Server:  godot --headless --path .
## Client:  godot --path . -- --connect=127.0.0.1:7777   (or leave --connect out and type the address)

const DEFAULT_PORT := 7777
const MAX_PLAYERS := 32
const SPEED := 200.0
const ARENA := Rect2(0, 0, 640, 360)

var positions: Dictionary = {}  # peer ID -> Vector2: decided on the server, last heard on a client
var wanted: Dictionary = {}  # on the server: peer ID -> the way that client wants to go
var someone_joined := false
var ending := false
var last_sent := Vector2.ZERO
var test_join := false  # --test-join: a client that joins, moves, prints a line and leaves
var start_x := -1.0


func _ready() -> void:
	if _arg("connect") == "" and (OS.has_feature("dedicated_server") or DisplayServer.get_name() == "headless"):
		_start_server()
	else:
		_start_client()


func _arg(name: String) -> String:
	for each in OS.get_cmdline_user_args():
		if each == "--" + name:
			return "yes"
		if each.begins_with("--" + name + "="):
			return each.substr(name.length() + 3)
	return ""


# The server ---------------------------------------------------------------------------------------------------------


func _start_server() -> void:
	Engine.max_fps = 30  # a game server with no frame cap spins a whole CPU thread doing nothing
	var port := GameServer.port("game", DEFAULT_PORT)
	var peer := ENetMultiplayerPeer.new()
	var err := peer.create_server(port, MAX_PLAYERS)
	if err != OK:
		printerr("listen on udp %d: %s" % [port, error_string(err)])
		get_tree().quit(1)
		return
	multiplayer.multiplayer_peer = peer
	multiplayer.peer_connected.connect(_on_player_joined)
	multiplayer.peer_disconnected.connect(_on_player_left)
	GameServer.shutdown_requested.connect(_on_stop_notice)
	var address := GameServer.public_address()
	print("listening on udp %d%s" % [port, "; players connect to " + address if address != "" else ""])
	if not await GameServer.ready_for_players():
		get_tree().quit(1)


func _on_player_joined(id: int) -> void:
	someone_joined = true
	positions[id] = ARENA.get_center()
	GameServer.player_connect(str(id))
	print("player %d joined" % id)


func _on_player_left(id: int) -> void:
	positions.erase(id)
	wanted.erase(id)
	GameServer.player_disconnect(str(id))
	print("player %d left" % id)
	# A match ends when everyone has left. A persistent server stays up, and so does a server you run by hand.
	if positions.is_empty() and someone_joined and GameServer.enabled and OS.get_environment("GS_KIND") not in ["persistent_server", "world"]:
		_end_match()


func _end_match() -> void:
	if ending:
		return
	ending = true
	print("match over")
	await GameServer.stop("match_over")
	get_tree().quit(0)


func _on_stop_notice(notice: Dictionary) -> void:
	# Save here if your game has anything to save: you have until notice["deadline"].
	print("asked to stop (%s)" % notice.get("reason", "api"))
	ending = true
	get_tree().quit(0)


func _physics_process(delta: float) -> void:
	if ending or multiplayer.multiplayer_peer == null or multiplayer.multiplayer_peer.get_connection_status() != MultiplayerPeer.CONNECTION_CONNECTED:
		return
	if multiplayer.is_server():
		for id in wanted:
			positions[id] = (positions[id] + wanted[id] * SPEED * delta).clamp(ARENA.position, ARENA.end)
		state.rpc(positions)
	else:
		var direction := Vector2.RIGHT if test_join else Input.get_vector("ui_left", "ui_right", "ui_up", "ui_down")
		if direction != last_sent:
			last_sent = direction
			want.rpc_id(1, direction)


@rpc("any_peer", "call_remote", "reliable")
func want(direction: Vector2) -> void:
	var id := multiplayer.get_remote_sender_id()
	if multiplayer.is_server() and positions.has(id):
		wanted[id] = direction.limit_length(1.0)  # never trust a client: it can ask, the server decides


# The client ---------------------------------------------------------------------------------------------------------


func _start_client() -> void:
	test_join = _arg("test-join") != ""
	var address := _arg("connect")
	if address != "":
		_join(address)
		return
	var box := LineEdit.new()
	box.text = "127.0.0.1:%d" % DEFAULT_PORT
	box.position = Vector2(16, 16)
	box.custom_minimum_size = Vector2(240, 0)
	box.text_submitted.connect(func(text: String) -> void:
		box.queue_free()
		_join(text))
	add_child(box)


func _join(address: String) -> void:
	var host := address.rsplit(":", true, 1)[0]
	var port := int(address.rsplit(":", true, 1)[1]) if address.contains(":") else DEFAULT_PORT
	var peer := ENetMultiplayerPeer.new()
	var err := peer.create_client(host, port)
	if err != OK:
		printerr("connect to %s: %s" % [address, error_string(err)])
		get_tree().quit(1)
		return
	multiplayer.multiplayer_peer = peer
	multiplayer.connected_to_server.connect(func() -> void: print("connected as player %d" % multiplayer.get_unique_id()))
	multiplayer.connection_failed.connect(func() -> void:
		printerr("could not reach %s" % address)
		get_tree().quit(1))
	multiplayer.server_disconnected.connect(func() -> void:
		print("the server closed")
		get_tree().quit(0))


@rpc("authority", "call_remote", "unreliable_ordered")
func state(all: Dictionary) -> void:
	positions = all
	queue_redraw()
	if not test_join:
		return
	var mine = positions.get(multiplayer.get_unique_id())
	if mine == null:
		return
	if start_x < 0.0:
		start_x = mine.x
	elif mine.x > start_x:
		print("joined and moved")
		get_tree().quit(0)


func _draw() -> void:
	draw_rect(ARENA, Color(0.12, 0.12, 0.13))
	for id in positions:
		var color := Color.GOLD if id == multiplayer.get_unique_id() else Color.GRAY
		draw_rect(Rect2(positions[id] - Vector2(10, 10), Vector2(20, 20)), color)
