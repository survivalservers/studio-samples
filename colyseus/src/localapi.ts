// The local API on the machine, over Node's own fetch. Every call is optional: without GS_LOCAL_API_URL (a run on your
// own PC) there is no client and the server runs as a plain local server.

export type Allocation = {
	game_server_id?: string;
	external_id?: string;
	labels?: Record<string, string>;
	env?: Record<string, string>;
	args?: string[];
};

export type Reply = { status: number; body: unknown };

export class LocalApi {
	constructor(
		readonly url: string,
		readonly token: string,
	) {}

	static fromEnv(): LocalApi | null {
		const url = process.env.GS_LOCAL_API_URL || process.env.GS_AGENT_URL || "";
		return url === "" ? null : new LocalApi(url.replace(/\/$/, ""), process.env.GS_TOKEN ?? "");
	}

	/** One call. Status 0 when it did not complete (the agent restarting, a closed connection). */
	async call(method: string, path: string, body?: unknown, timeoutMs = 40_000): Promise<Reply> {
		const headers: Record<string, string> = { Authorization: `Bearer ${this.token}` };
		if (body !== undefined) headers["Content-Type"] = "application/json";
		try {
			const res = await fetch(this.url + path, {
				method,
				headers,
				body: body === undefined ? undefined : JSON.stringify(body),
				signal: AbortSignal.timeout(timeoutMs),
			});
			const text = await res.text();
			let parsed: unknown = text;
			try {
				parsed = text === "" ? null : JSON.parse(text);
			} catch {
				// not JSON: keep the text
			}
			return { status: res.status, body: parsed };
		} catch {
			return { status: 0, body: null };
		}
	}

	ready = () => this.call("POST", "/v0/self/ready");
	health = () => this.call("POST", "/v0/self/health");
	/** The match: 200 with it, 204 when there is none yet (a ready server waits here, up to `wait`). */
	allocation = (wait: string) => this.call("GET", `/v0/self/allocation?wait=${wait}`);
	/** 200 with the shutdown notice once a stop is asked for, 204 when `wait` ran out first. */
	shutdown = (wait: string) => this.call("GET", `/v0/self/shutdown?wait=${wait}`);
	players = (ids: string[]) => this.call("PUT", "/v0/self/players", { ids });
	tick = (p50: number, p99: number, hz: number) => this.call("POST", "/v0/self/tick", { p50_ms: p50, p99_ms: p99, hz });
	stop = (reason: string) => this.call("POST", "/v0/self/stop", { reason });
}

/** The platform refuses a player ID of more than 128 characters or with a control character. */
export function badPlayerId(id: string): boolean {
	const chars = [...id];
	return chars.length === 0 || chars.length > 128 || chars.some((c) => {
		const n = c.codePointAt(0) ?? 0;
		return n < 0x20 || (n >= 0x7f && n <= 0x9f);
	});
}
