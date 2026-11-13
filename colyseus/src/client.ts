// A scripted player for the sample, over Colyseus' own client SDK: each name joins the "match" room, says hello,
// pings the server, waits, then leaves (or, with --stay, stays until the server ends the connection). It prints one
// JSON line with what came back, and exits 0 only when every player joined and got its welcome and a pong.
//
//   node dist/client.cjs ws://HOST:PORT [--players ana,bo] [--hold SECONDS] [--stay SECONDS]
import { Client, type Room } from "@colyseus/sdk";

type Player = { name: string; room?: Room; welcome?: string; pong_ms?: number; tick?: number; error?: string; closed?: number };

function arg(name: string, fallback: string): string {
	const i = process.argv.indexOf(name);
	return i > 0 && i + 1 < process.argv.length ? process.argv[i + 1] : fallback;
}

const url = process.argv[2];
if (!url || url.startsWith("--")) {
	console.error("usage: client.cjs ws://HOST:PORT [--players ana,bo] [--hold SECONDS] [--stay SECONDS]");
	process.exit(2);
}
const names = arg("--players", "ana").split(",").filter((n) => n !== "");
const hold = Number(arg("--hold", "0")) * 1000;
const stay = process.argv.includes("--stay") ? Number(arg("--stay", "60")) * 1000 : 0;
const sleep = (ms: number) => new Promise((ok) => setTimeout(ok, ms));
const timeout = <T>(p: Promise<T>, ms: number, what: string) =>
	Promise.race([p, sleep(ms).then(() => Promise.reject(new Error(`${what}: no answer in ${ms} ms`)))]);

async function join(p: Player) {
	const client = new Client(url);
	const room = await timeout(client.joinOrCreate("match", { name: p.name }), 10_000, "join");
	p.room = room;
	room.onLeave((code: number) => (p.closed = code));
	p.welcome = await timeout(new Promise<string>((ok) => {
		room.onMessage("welcome", ok);
		room.send("hello");
	}), 10_000, "welcome");
	const t0 = performance.now();
	const pong = await timeout(new Promise<{ tick: number }>((ok) => {
		room.onMessage("pong", ok);
		room.send("ping");
	}), 10_000, "pong");
	p.pong_ms = Number((performance.now() - t0).toFixed(1));
	p.tick = pong.tick;
}

async function main() {
	const players: Player[] = names.map((name) => ({ name }));
	const t0 = Date.now();
	for (const p of players) {
		await join(p).catch((e: Error) => (p.error = e.message));
	}
	if (hold > 0) await sleep(hold);
	if (stay > 0) {
		// Wait for the server to end the connections (a stop), or give up.
		const end = Date.now() + stay;
		while (Date.now() < end && players.some((p) => p.room && p.closed === undefined)) await sleep(100);
	} else {
		for (const p of players) {
			if (p.room) p.closed = await p.room.leave(true).catch(() => -1);
		}
	}
	const ok = players.every((p) => p.error === undefined && p.welcome && p.pong_ms !== undefined);
	const out = players.map(({ room: _room, ...rest }) => rest);
	console.log(JSON.stringify({ ok, url, ms: Date.now() - t0, players: out }));
	process.exit(ok ? 0 : 1);
}

void main();
