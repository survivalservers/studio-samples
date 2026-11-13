// A sample game server on Colyseus (Node.js, TypeScript) with no SDK. Everything the platform offers is plain HTTP
// on the local API, so Node's own fetch does it all:
//
// - the port and the match settings come from GS_* environment variables and the allocation call;
// - it says it is ready on the local API (or a version can watch for the line it prints);
// - pre-started as a ready server, it waits for its match on a long-poll;
// - it reports its players and its tick times, ends the match by stopping itself when the last player leaves, and
//   saves on the way out when asked to stop (the shutdown notice, SIGTERM on Linux, Ctrl+Break on Windows);
// - it leaves a result file to be collected after it exits.
//
// The "game" is one Colyseus room, "match": join it with the option `name`; "hello" answers "welcome", and "ping"
// answers "pong" with the server's tick. With no GS_* variables it runs as a plain local server on port 2567.
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { defineRoom, defineServer, Room, ServerError, type Client } from "@colyseus/core";
import { WebSocketTransport } from "@colyseus/ws-transport";
import { type Allocation, badPlayerId, LocalApi } from "./localapi.ts";

const READY_LINE = "Server ready for players";
const LONG_POLL = "30s";
const HZ = 20;

const api = LocalApi.fromEnv();
const gsId = process.env.GS_ID ?? "local";
const started = Date.now();

let allocation: Allocation | null = null;
let ending = false;
let peak = 0;
const players = new Map<string, Client>(); // name -> its connection

/** Linux images listen on the container port; Windows builds bind the public port itself. */
function listenPort(): number {
	for (const name of ["GS_PORT_GAME_INTERNAL", "GS_PORT_GAME"]) {
		const n = Number(process.env[name]);
		if (Number.isInteger(n) && n > 0) return n;
	}
	return 2567;
}

/** The player list, one call at a time; a change during a call sends the newest list after it. */
let reporting: Promise<void> | null = null;
let reportAgain = false;
function reportPlayers(): Promise<void> {
	if (!api) return Promise.resolve();
	if (reporting) {
		reportAgain = true;
		return reporting;
	}
	reporting = (async () => {
		do {
			reportAgain = false;
			await api.players([...players.keys()]);
		} while (reportAgain);
		reporting = null;
	})();
	return reporting;
}

class MatchRoom extends Room {
	maxClients = 64;
	private tick = 0;
	private frames: number[] = [];

	onCreate() {
		// The match runs at a fixed rate; each second's frame times go to the game server's tick graph.
		this.setSimulationInterval(() => {
			const t0 = performance.now();
			this.tick++; // the match's own update goes here
			this.frames.push(performance.now() - t0);
			if (this.frames.length >= HZ) {
				const sorted = this.frames.sort((a, b) => a - b);
				const at = (q: number) => Number(sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))].toFixed(3));
				void api?.tick(at(0.5), at(0.99), HZ);
				this.frames = [];
			}
		}, 1000 / HZ);
		// A message sent during the join can arrive before the player's handler exists, so the welcome answers a hello.
		this.onMessage("hello", (client) => client.send("welcome", `welcome ${(client.userData as { name: string }).name} to ${gsId}`));
		this.onMessage("ping", (client) => client.send("pong", { tick: this.tick }));
	}

	onJoin(client: Client, options: { name?: unknown }) {
		const name = typeof options?.name === "string" ? options.name.trim() : "";
		// The platform refuses a player list holding such a name, so it never joins; the others keep counting.
		if (badPlayerId(name)) throw new ServerError(422, "refused: a name is 1 to 128 characters with no control characters");
		if (players.has(name)) throw new ServerError(409, `already in ${gsId}`);
		players.set(name, client);
		client.userData = { name };
		peak = Math.max(peak, players.size);
		console.log(`${name} joined (${players.size} playing)`);
		void reportPlayers();
	}

	onLeave(client: Client) {
		const name = (client.userData as { name?: string } | undefined)?.name;
		if (name === undefined || players.get(name) !== client) return;
		players.delete(name);
		console.log(`${name} left (${players.size} playing)`);
		void reportPlayers();
		if (players.size === 0 && !ending) void finish("match_over", true);
	}
}

const server = defineServer({
	transport: new WebSocketTransport(),
	rooms: { match: defineRoom(MatchRoom) },
	gracefullyShutdown: false, // our own handlers below: they save first, and Windows sends SIGBREAK
	greet: false,
});

function setAllocation(data: Allocation) {
	allocation = data;
	// Setting names only: values can be secret, and the console is a log kept for 90 days.
	console.log(`allocated: match "${data.external_id ?? ""}", labels ${JSON.stringify(data.labels ?? {})}, settings ${JSON.stringify(Object.keys(data.env ?? {}))}`);
}

/** match-result.json in GS_ARTIFACT_DIR is collected after the game server exits. */
function writeResult(reason: string, seconds: number) {
	const dir = process.env.GS_ARTIFACT_DIR;
	if (!dir) return;
	const result: Record<string, unknown> = { game_server_id: gsId, reason, peak_players: peak, seconds };
	if (allocation) result.match = allocation.external_id ?? "";
	writeFileSync(join(dir, "match-result.json"), JSON.stringify(result, null, 2) + "\n");
}

/** Ends the match: the last player list goes out, the result is saved, and if the game server decided to end (rather
 *  than being asked to stop) it tells the platform, which is the same as exiting with code 0. */
async function finish(reason: string, selfStop: boolean) {
	if (ending) return;
	ending = true;
	if (reporting) await reporting;
	const seconds = Math.round((Date.now() - started) / 1000);
	console.log(`match over: ${reason} after ${seconds}s, ${peak} players at most`);
	writeResult(reason, seconds);
	if (selfStop && api) await api.stop(reason);
	await server.gracefullyShutdown(false).catch(() => {});
	process.exit(0);
}

async function waitForAllocation() {
	while (allocation === null && !ending && api) {
		const r = await api.allocation(LONG_POLL);
		if (r.status === 200 && r.body && typeof r.body === "object") setAllocation(r.body as Allocation);
		else if (r.status !== 204) await new Promise((ok) => setTimeout(ok, 1000));
	}
}

async function waitForStop() {
	while (!ending && api) {
		const r = await api.shutdown(LONG_POLL);
		if (r.status === 200) {
			console.log("stop asked; saving and leaving");
			return finish("stop_asked", false);
		}
		if (r.status !== 204 && !ending) await new Promise((ok) => setTimeout(ok, 1000));
	}
}

// A stop signal (SIGTERM on Linux, Ctrl+Break as SIGBREAK on Windows, Ctrl+C): save and leave.
for (const sig of ["SIGTERM", "SIGINT", "SIGBREAK"] as const) {
	process.on(sig, () => {
		console.log(`${sig}; saving and leaving`);
		void finish("stop_signal", false);
	});
}

async function main() {
	const port = listenPort();
	await server.listen(port);
	console.log(`listening on tcp ${port} (WebSocket); players connect to ws://${process.env.GS_PUBLIC_IP ?? "127.0.0.1"}:${process.env.GS_PORT_GAME ?? port}`);
	if (!api) {
		console.log(READY_LINE);
		return;
	}
	// A fresh start is allocated before it starts: read the match now, before saying ready, so it is set up before the
	// first player arrives. A ready server (pre-started) has none yet (204) and waits for it after ready.
	const first = await api.allocation("0s");
	if (first.status === 200 && first.body && typeof first.body === "object") setAllocation(first.body as Allocation);
	// A version can use this exact line as its ready check instead of the call below.
	console.log(READY_LINE);
	const ready = await api.ready();
	if (ready.status < 200 || ready.status >= 300) {
		console.error(`say ready: ${ready.status} ${JSON.stringify(ready.body)}`);
		process.exit(1);
	}
	void waitForAllocation();
	void waitForStop();
}

main().catch((err) => {
	console.error(err);
	process.exit(1);
});
