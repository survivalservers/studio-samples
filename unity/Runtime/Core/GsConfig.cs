using System;

namespace GameServerSample.Core
{
	/// <summary>What the game server learns from its environment. Missing values fall back to a local run on port 7777.</summary>
	public sealed class GsConfig
	{
		public string Id = "local";             // GS_ID
		public string PublicIp = "";            // GS_PUBLIC_IP: the address players use
		public int PublicPort;                  // GS_PORT_GAME: the port players use
		public int ListenPort = 7777;           // GS_PORT_GAME_INTERNAL in a Linux image, else GS_PORT_GAME
		public string AgentUrl = "";            // GS_LOCAL_API_URL (or its older name GS_AGENT_URL)
		public string Token = "";               // GS_TOKEN
		public string AllocationFile = "";      // GS_ALLOCATION_FILE
		public string ArtifactDir = "";         // GS_ARTIFACT_DIR: files here are collected after exit

		public static GsConfig FromEnvironment() => From(Environment.GetEnvironmentVariable);

		public static GsConfig From(Func<string, string> getenv)
		{
			string Get(string name) => getenv(name) ?? "";
			int Port(string name)
			{
				var v = Get(name);
				if (v.Length == 0) return 0;
				if (!int.TryParse(v, out var n) || n < 0 || n > 65535) throw new FormatException($"{name}={v} is not a port");
				return n;
			}

			var c = new GsConfig
			{
				PublicIp = Get("GS_PUBLIC_IP"),
				AgentUrl = (Get("GS_LOCAL_API_URL") != "" ? Get("GS_LOCAL_API_URL") : Get("GS_AGENT_URL")).TrimEnd('/'),
				Token = Get("GS_TOKEN"),
				AllocationFile = Get("GS_ALLOCATION_FILE"),
				ArtifactDir = Get("GS_ARTIFACT_DIR"),
				PublicPort = Port("GS_PORT_GAME"),
			};
			if (Get("GS_ID").Length > 0) c.Id = Get("GS_ID");
			var internalPort = Port("GS_PORT_GAME_INTERNAL");
			c.ListenPort = internalPort != 0 ? internalPort : c.PublicPort != 0 ? c.PublicPort : 7777;
			return c;
		}
	}
}
