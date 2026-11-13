@tool
extends EditorPlugin
## Turning the add-on on adds the GameServer autoload; turning it off removes it. Nothing else runs in the editor.

const AUTOLOAD := "GameServer"


func _enable_plugin() -> void:
	add_autoload_singleton(AUTOLOAD, get_script().resource_path.get_base_dir().path_join("game_server.gd"))


func _disable_plugin() -> void:
	remove_autoload_singleton(AUTOLOAD)
