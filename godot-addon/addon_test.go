// Runs the Survival Servers Studio add-on headless: the demo project against a fake local API and on its own, and the
// add-on copied into a fresh project and turned on in the editor as a person would. Needs Godot 4: set GODOT to its
// program, or have godot or godot4 on the PATH; skipped otherwise. No window is opened.
package godotaddon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/survivalservers/studio-samples/internal/fakeagent"
	"github.com/survivalservers/studio-samples/internal/godotrun"
)

func demo(t *testing.T, env ...string) *godotrun.Run {
	t.Helper()
	program := godotrun.Program(t)
	project := godotrun.Copy(t, ".", "project.godot", "addons", "demo")
	return godotrun.Start(t, program, env, "--path", project)
}

func lastPlayers(calls []string) string {
	for i := len(calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(calls[i], "PUT ") {
			return calls[i]
		}
	}
	return ""
}

func TestDemoWarmMatchFromReadyToSelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_addon", "external_id": "m-7",
		"env": map[string]string{"MATCH_MODE": "Duo", "BACKEND_TOKEN": "s3cr3t-value"}})
	defer agent.Close()
	r := demo(t, "GS_ID=gs_addon", "GS_LOCAL_API_URL="+agent.URL, "GS_TOKEN="+agent.Token)
	r.WaitFor(t, `allocated: match "m-7"`)

	if got := r.Send(t, "join ana"); got != "welcome ana to gs_addon" {
		t.Fatalf("join: %q", got)
	}
	r.Send(t, "join bo")
	if got := r.Send(t, "ping"); got != "pong" {
		t.Fatalf("ping: %q", got)
	}
	time.Sleep(1300 * time.Millisecond) // one round of update times
	r.Send(t, "leave ana")
	r.Send(t, "leave bo")
	r.Exit(t)

	calls := agent.Calls()
	if calls[0] != "POST /v0/self/ready" || calls[len(calls)-1] != "POST stop match_over" {
		t.Fatalf("want ready first and a self-stop last; got %v", calls)
	}
	if got := lastPlayers(calls); got != "PUT players " {
		t.Fatalf("the empty player list goes out before the stop; last players call %q in %v", got, calls)
	}
	for _, want := range []string{"PUT players ana", "PUT players ana,bo", "PATCH labels mode=duo", "POST tick hz=60"} {
		if !agent.Has(want) {
			t.Errorf("no %q in %v", want, calls)
		}
	}
	if strings.Contains(r.Output(), "s3cr3t-value") || !strings.Contains(r.Output(), "BACKEND_TOKEN") {
		t.Errorf("setting names reach the log, never their values:\n%s", r.Output())
	}
	if res := r.Result(t); res["reason"] != "match_over" || res["peak_players"] != float64(2) || res["match"] != "m-7" {
		t.Fatalf("result: %v", res)
	}
}

func TestDemoStopNoticeSavesAndLeavesWithoutASelfStop(t *testing.T) {
	agent := fakeagent.New("tok", map[string]any{"game_server_id": "gs_addon"})
	defer agent.Close()
	r := demo(t, "GS_ID=gs_addon", "GS_AGENT_URL="+agent.URL, "GS_TOKEN="+agent.Token) // the older name still reaches it
	r.WaitFor(t, "allocated:")
	r.Send(t, "join cy")
	agent.AskStop()
	r.Exit(t)
	for _, c := range agent.Calls() {
		if strings.HasPrefix(c, "POST stop") {
			t.Fatalf("asked to stop, the server must not stop itself as well: %v", agent.Calls())
		}
	}
	if res := r.Result(t); res["reason"] != "stop_asked" {
		t.Fatalf("result: %v", res)
	}
}

func TestDemoRunsAsAPlainLocalServerWithoutThePlatform(t *testing.T) {
	r := demo(t) // no GS_LOCAL_API_URL: every call does nothing
	r.WaitFor(t, "Server ready for players")
	if got := r.Send(t, "join dee"); got != "welcome dee to local" {
		t.Fatalf("join: %q", got)
	}
	r.Send(t, "leave dee")
	r.Exit(t)
	if res := r.Result(t); res["reason"] != "match_over" {
		t.Fatalf("result: %v", res)
	}
}

// The editor helper: turns the add-on on as Project Settings > Plugins does, checks that turning it off removes the
// autoload again, turns it back on, saves and quits.
const enabler = `@tool
extends EditorPlugin


func _enter_tree() -> void:
	_go.call_deferred()


func _go() -> void:
	# As a person would: once the editor has looked through the project's files.
	var files := EditorInterface.get_resource_filesystem()
	while files.is_scanning() or files.get_filesystem().get_file_count() == 0 and files.get_filesystem().get_subdir_count() == 0:
		await get_tree().process_frame
	EditorInterface.set_plugin_enabled("game_server", true)
	print("on: ", ProjectSettings.has_setting("autoload/GameServer"))
	EditorInterface.set_plugin_enabled("game_server", false)
	print("off: ", ProjectSettings.has_setting("autoload/GameServer"))
	EditorInterface.set_plugin_enabled("game_server", true)
	ProjectSettings.save()
	get_tree().quit()
`

// A game that uses the add-on and nothing else.
const game = `extends Node


func _ready() -> void:
	if await GameServer.ready_for_players():
		print("installed: ", GameServer.id)
		await GameServer.stop("installed")
	get_tree().quit(0)
`

func TestInstallingTheAddOnIntoAFreshProject(t *testing.T) {
	program := godotrun.Program(t)
	project := godotrun.Copy(t, ".", filepath.Join("addons", "game_server"))
	files := map[string]string{
		"project.godot":             "config_version=5\n\n[application]\n\nconfig/name=\"Install check\"\nrun/main_scene=\"res://game.tscn\"\n\n[editor_plugins]\n\nenabled=PackedStringArray(\"res://addons/enabler/plugin.cfg\")\n",
		"addons/enabler/plugin.cfg": "[plugin]\n\nname=\"Enabler\"\ndescription=\"Test helper\"\nauthor=\"test\"\nversion=\"1\"\nscript=\"enabler.gd\"\n",
		"addons/enabler/enabler.gd": enabler,
		"game.gd":                   game,
		"game.tscn":                 "[gd_scene load_steps=2 format=3]\n\n[ext_resource type=\"Script\" path=\"res://game.gd\" id=\"1\"]\n\n[node name=\"Game\" type=\"Node\"]\nscript = ExtResource(\"1\")\n",
	}
	for name, text := range files {
		path := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	editor := godotrun.Start(t, program, nil, "--editor", "--path", project)
	editor.Exit(t)
	if out := editor.Output(); !strings.Contains(out, "on: true") || !strings.Contains(out, "off: false") {
		t.Fatalf("turning the add-on on adds the autoload and turning it off removes it:\n%s", out)
	}
	settings, err := os.ReadFile(filepath.Join(project, "project.godot"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^GameServer="\*`).Match(settings) || !strings.Contains(string(settings), "res://addons/game_server/plugin.cfg") {
		t.Fatalf("project.godot after turning the add-on on:\n%s", settings)
	}

	agent := fakeagent.New("tok", nil)
	defer agent.Close()
	run := godotrun.Start(t, program, []string{"GS_ID=gs_installed", "GS_LOCAL_API_URL=" + agent.URL, "GS_TOKEN=" + agent.Token}, "--path", project)
	run.Exit(t)
	if !strings.Contains(run.Output(), "installed: gs_installed") || !agent.Has("POST /v0/self/ready") || !agent.Has("POST stop installed") {
		t.Fatalf("the installed add-on reaches the local API; calls %v\n%s", agent.Calls(), run.Output())
	}
}
