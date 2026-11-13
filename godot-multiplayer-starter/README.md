# Godot multiplayer starter

Godot's multiplayer samples run on your own computer. This one also runs as a dedicated server on Windows or Linux, with one command from clone to an address your players can join.

```bash
export GS_API_URL=https://studio.survivalservers.com GS_API_KEY=gsk_live_...
./deploy.sh windows
```

```text
Game server gs_...: running in europe-west
Address: 203.0.113.24:31042
```

Then join it: `godot --path . -- --connect=203.0.113.24:31042`

## What you get

- One project for the client and the server, with ENet and a server that decides every move
- Windows and Linux builds, with a version file for each
- The Survival Servers Studio add-on: the port, the players and the shutdown notice
- `AGENTS.md`, `CLAUDE.md` and the MCP settings for Claude Code, Codex, Cursor and VS Code, so your AI tool can run all of it for you

## Run it on your computer

```bash
godot --headless --path .                          # the server, on UDP port 7777
godot --path . -- --connect=127.0.0.1:7777         # a client; arrow keys move
```

## Use it with Claude Code or Cursor

Open this folder and paste:

> Start this Godot project as a Windows dedicated server in Europe West with deploy.sh, and give me the address. Then tell me how to stop it.

Apache-2.0.
