// Bundles the server and the scripted client, each into one file with its packages inside, so a build is that file
// and Node: dist/server.cjs (the game server) and dist/client.cjs (a scripted player). `npm run build` runs it.
import { build } from "esbuild";

for (const name of ["server", "client"]) {
	await build({
		entryPoints: [`src/${name}.ts`],
		outfile: `dist/${name}.cjs`,
		bundle: true,
		platform: "node",
		target: "node22",
		format: "cjs",
		// Optional packages Colyseus and ws load only when they are installed (uWebSockets, native WebSocket speed-ups,
		// PM2, validation, sign-in); everything else, Express included (the WebSocket transport imports it), is inside.
		external: ["uwebsockets-express", "uWebSockets.js", "bufferutil", "utf-8-validate", "@pm2/io", "zod", "@colyseus/auth"],
		logLevel: "warning",
	});
}
console.log("dist/server.cjs and dist/client.cjs");
