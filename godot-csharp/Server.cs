// A headless Godot .NET game server with the C# SDK: Godot runs the match at 30 frames a second, a UDP socket carries
// the players, and the SDK tells the platform it is ready, who is playing and how long each frame takes. It stops
// when the platform asks (the shutdown notice) or when the last player leaves.
//
// Players send `join <id>`, `leave <id>` and `ping`; the server answers `welcome <id>`, `bye <id>`, `pong <frame>`.
// Without the platform (no GS_LOCAL_API_URL) it runs as a plain local server.
//
// The SDK calls the C library, so libgs_sdk.so (Linux, on LD_LIBRARY_PATH) or gs_sdk.dll (Windows, beside the Godot
// program) must be where .NET looks for native libraries.
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using GameServerSdk;
using Godot;

public partial class Server : Node
{
	const int Hz = 30;

	UdpClient udp;
	Client gs;
	volatile string stopReason;
	readonly List<string> players = new();
	readonly List<double> frames = new();
	readonly Stopwatch frameTime = new();
	ulong frame;
	double sinceReport;
	int mostPlayers;
	string endWhy; // set once the match is over; the frame that sees it answers its last packets, then quits
	bool selfStop;

	public override void _Ready()
	{
		// A headless server needs no more frames than its tick, and on Windows an uncapped Godot spins between them.
		Engine.MaxFps = Hz;
		// Inside a Linux container the game listens on its fixed port (GS_PORT_GAME_INTERNAL) and the platform maps the
		// public one onto it; on Windows it listens on the public port itself (GS_PORT_GAME).
		var port = new[] { "GS_PORT_GAME_INTERNAL", "GS_PORT_GAME" }
			.Select(n => int.TryParse(System.Environment.GetEnvironmentVariable(n), out var p) ? p : 0)
			.FirstOrDefault(p => p > 0, 7777);
		udp = new UdpClient(new IPEndPoint(IPAddress.Any, port));
		GD.Print($"listening on udp {port}");
		if (string.IsNullOrEmpty(System.Environment.GetEnvironmentVariable("GS_LOCAL_API_URL")) && string.IsNullOrEmpty(System.Environment.GetEnvironmentVariable("GS_AGENT_URL")))
		{
			GD.Print("no platform; running as a plain local server");
			return;
		}
		gs = Client.FromEnvironment();
		var match = gs.WaitAllocation(TimeSpan.Zero);
		if (match != null)
			GD.Print($"allocated: match \"{match.ExternalId}\"");
		gs.Ready();
		GD.Print("Server ready for players");
		// The shutdown notice comes on the local API; each frame checks for it.
		new Thread(() =>
		{
			while (stopReason == null)
			{
				var sd = gs.WaitShutdown(TimeSpan.FromSeconds(60));
				if (sd != null)
					stopReason = sd.Reason;
			}
		}) { IsBackground = true }.Start();
	}

	public override void _Process(double delta)
	{
		frameTime.Restart();
		while (udp.Available > 0)
		{
			var from = new IPEndPoint(IPAddress.Any, 0);
			var reply = Handle(Encoding.UTF8.GetString(udp.Receive(ref from)).Trim());
			if (reply != null)
			{
				var b = Encoding.UTF8.GetBytes(reply);
				udp.Send(b, b.Length, from);
			}
		}
		frame++;
		frames.Add(frameTime.Elapsed.TotalMilliseconds);
		sinceReport += delta;
		if (gs != null && sinceReport >= 10)
		{
			frames.Sort();
			Call(() => gs.Tick(frames[frames.Count / 2], frames[(int)(frames.Count * 0.99)], Hz));
			frames.Clear();
			sinceReport = 0;
		}
		if (stopReason != null)
			End($"shutdown notice: {stopReason}", false);
		if (endWhy != null)
			Finish();
	}

	string Handle(string line)
	{
		var parts = line.Split(' ', 2);
		switch (parts[0])
		{
			case "join" when parts.Length == 2:
				if (!players.Contains(parts[1]))
				{
					players.Add(parts[1]);
					mostPlayers = Math.Max(mostPlayers, players.Count);
					Call(() => gs?.PlayerConnect(parts[1]));
				}
				GD.Print($"{parts[1]} joined ({players.Count} playing)");
				return $"welcome {parts[1]}";
			case "leave" when parts.Length == 2:
				if (players.Remove(parts[1]))
					Call(() => gs?.PlayerDisconnect(parts[1]));
				GD.Print($"{parts[1]} left ({players.Count} playing)");
				if (players.Count == 0 && mostPlayers > 0)
					End("match over: everyone left", true);
				return $"bye {parts[1]}";
			case "ping":
				return $"pong {frame}";
		}
		return null;
	}

	// End marks the match over: a self-stop when the game decides, or quitting after the platform's shutdown notice.
	void End(string why, bool self)
	{
		if (endWhy != null)
			return;
		endWhy = why;
		selfStop = self;
	}

	void Finish()
	{
		GD.Print($"{endWhy} after {frame / Hz} s, {mostPlayers} players at most");
		if (selfStop)
			Call(() => gs?.Stop("match_over"));
		gs?.Dispose();
		gs = null;
		udp.Close();
		SetProcess(false);
		GetTree().Quit(0);
	}

	static void Call(Action call)
	{
		try
		{
			call();
		}
		catch (GsException e)
		{
			GD.PrintErr($"gs: {e.Code} {e.Message}");
		}
	}
}
