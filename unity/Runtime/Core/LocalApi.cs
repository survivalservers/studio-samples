// Plain C# with no UnityEngine types, so it runs in Unity (Mono or IL2CPP) and in plain .NET tests alike.
using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;

namespace GameServerSample.Core
{
	/// <summary>One HTTP reply from the local API. Status 0 means the call did not complete.</summary>
	public readonly struct LocalReply
	{
		public readonly int Status;
		public readonly string Body;

		public LocalReply(int status, string body)
		{
			Status = status;
			Body = body ?? "";
		}

		public bool Ok => Status >= 200 && Status < 300;
	}

	/// <summary>
	/// How calls reach the local API. In Unity the sample uses UnityWebRequest; <see cref="HttpClientTransport"/>
	/// works in Unity too and is what the tests use.
	/// </summary>
	public interface ILocalTransport
	{
		/// <param name="path">For example <c>/v0/self/ready</c>.</param>
		/// <param name="json">The request body, or null.</param>
		Task<LocalReply> Send(string method, string path, string json, CancellationToken ct);
	}

	/// <summary>The match data from the allocation call (the allocation file has the same JSON without secret values).</summary>
	public sealed class Allocation
	{
		[JsonProperty("game_server_id")] public string GameServerId;
		[JsonProperty("external_id")] public string ExternalId;
		[JsonProperty("labels")] public Dictionary<string, string> Labels = new Dictionary<string, string>();
		[JsonProperty("env")] public Dictionary<string, string> Env = new Dictionary<string, string>();
		[JsonProperty("args")] public List<string> Args = new List<string>();
	}

	/// <summary>A shutdown notice: save and exit before the deadline.</summary>
	public sealed class Shutdown
	{
		[JsonProperty("deadline")] public DateTime Deadline;
		/// <summary>Why: api, bulk, self, ttl, max_duration, health_timeout, restart and others.</summary>
		[JsonProperty("reason")] public string Reason = "";
	}

	/// <summary>
	/// The calls a game server can make to the local API at $GS_LOCAL_API_URL with $GS_TOKEN, under the same names as
	/// the custom-engine SDK (C <c>gs_ready</c>, C# <c>Ready()</c> and so on). All optional: a game server that makes none
	/// still works. With no transport (no GS_LOCAL_API_URL) every call does nothing.
	/// </summary>
	public sealed class LocalApi
	{
		/// <summary>The longest a wait can be: the local API answers by then with "nothing yet".</summary>
		public static readonly TimeSpan MaxWait = TimeSpan.FromSeconds(60);

		/// <summary>How long a transport waits for any answer: longer than the longest wait, so a long-poll is never cut
		/// off before the local API answers it.</summary>
		public static readonly TimeSpan TransportTimeout = MaxWait + TimeSpan.FromSeconds(10);

		private readonly ILocalTransport _transport;
		private readonly object _gate = new object();
		private readonly SortedSet<string> _players = new SortedSet<string>(StringComparer.Ordinal);
		private Task<LocalReply> _reports = Task.FromResult(new LocalReply(200, ""));

		public LocalApi(ILocalTransport transport) => _transport = transport;

		public bool Enabled => _transport != null;

		/// <summary>Ready for players (for a ready server: ready to be picked for a match).</summary>
		public async Task Ready(CancellationToken ct = default)
		{
			if (!Enabled) return;
			var reply = await _transport.Send("POST", "/v0/self/ready", null, ct);
			if (!reply.Ok) throw new InvalidOperationException($"ready call failed: {reply.Status} {reply.Body}");
		}

		/// <summary>The optional heartbeat, for versions that set health_timeout.</summary>
		public Task<LocalReply> Health(CancellationToken ct = default) => Send("POST", "/v0/self/health", null, 204, ct);

		/// <summary>Ends this game server on purpose, the same as exiting with code 0.</summary>
		public Task<LocalReply> Stop(string reason, CancellationToken ct = default) =>
			Send("POST", "/v0/self/stop", JsonConvert.SerializeObject(new { reason }), 202, ct);

		/// <summary>
		/// A persistent server just finished a save: it is backed up at once, and with <paramref name="keep"/>, kept under that
		/// name. Optional: saves are backed up without it. An agent older than this call answers 404, which is not an error.
		/// </summary>
		public Task<LocalReply> Saved(string keep = null, CancellationToken ct = default) =>
			Send("POST", "/v0/self/saved", keep == null ? "{}" : JsonConvert.SerializeObject(new { keep }), 202, ct);

		/// <summary>
		/// Waits up to <paramref name="wait"/> (at most 60 seconds) for this game server's match: the allocation, or null
		/// when there is none yet (call again) or a stop arrived first.
		/// </summary>
		public async Task<Allocation> WaitAllocation(TimeSpan wait, CancellationToken ct = default)
		{
			if (!Enabled) return null;
			var reply = await _transport.Send("GET", "/v0/self/allocation?wait=" + Seconds(wait), null, ct);
			return reply.Status == 200 ? JsonConvert.DeserializeObject<Allocation>(reply.Body) : null;
		}

		/// <summary>Waits up to <paramref name="wait"/> (at most 60 seconds) for a shutdown notice; null when there is none.</summary>
		public async Task<Shutdown> WaitShutdown(TimeSpan wait, CancellationToken ct = default)
		{
			if (!Enabled) return null;
			var reply = await _transport.Send("GET", "/v0/self/shutdown?wait=" + Seconds(wait), null, ct);
			return reply.Status == 200 ? JsonConvert.DeserializeObject<Shutdown>(reply.Body) : null;
		}

		/// <summary>
		/// A player joined. The package keeps the list and sends it whole, in order, after every change, so the count
		/// the platform sees (which also tells a persistent server when nobody plays) is never zero while someone does.
		/// An ID is 1 to 128 characters with no control characters; any other (often a name a player chose) is refused
		/// (status 422, nothing sent) and never joins the list, so the others keep counting.
		/// </summary>
		public Task<LocalReply> PlayerConnect(string id, CancellationToken ct = default)
		{
			var problem = PlayerIdProblem(id);
			return problem != null ? Task.FromResult(new LocalReply(422, "the player ID " + problem)) : ChangePlayers(id, true, ct);
		}

		/// <summary>What is wrong with a player ID, or null: the agent's rule, 1 to 128 characters with no control
		/// characters (U+0000-U+001F, U+007F-U+009F). The agent refuses a list holding such an ID whole.</summary>
		public static string PlayerIdProblem(string id)
		{
			if (string.IsNullOrEmpty(id)) return "is empty";
			var n = 0;
			for (var i = 0; i < id.Length; i++, n++)
			{
				var c = id[i];
				if (c < 0x20 || (c >= 0x7f && c <= 0x9f)) return "has a control character";
				if (char.IsHighSurrogate(c) && i + 1 < id.Length && char.IsLowSurrogate(id[i + 1])) i++; // one character
			}
			return n > 128 ? "is longer than 128 characters" : null;
		}

		/// <summary>A player left.</summary>
		public Task<LocalReply> PlayerDisconnect(string id, CancellationToken ct = default) => ChangePlayers(id, false, ct);

		/// <summary>The players as the package last sent them.</summary>
		public IReadOnlyCollection<string> ConnectedPlayers
		{
			get { lock (_gate) return _players.ToArray(); }
		}

		/// <summary>The latest player report, for waiting on it before the process exits.</summary>
		public Task<LocalReply> Reported
		{
			get { lock (_gate) return _reports; }
		}

		/// <summary>For games without player IDs: how many are playing.</summary>
		public Task<LocalReply> SetPlayerCount(int count, CancellationToken ct = default) =>
			Send("PUT", "/v0/self/players", JsonConvert.SerializeObject(new { count }), 200, ct);

		/// <summary>The low-level call: replaces the player list with these IDs.</summary>
		public Task<LocalReply> Players(IReadOnlyCollection<string> ids, CancellationToken ct = default) =>
			Send("PUT", "/v0/self/players", JsonConvert.SerializeObject(new { ids }), 200, ct);

		/// <summary>Sets one of this game server's labels, which the studio's backend can filter on.</summary>
		public Task<LocalReply> SetLabel(string name, string value, CancellationToken ct = default) =>
			Send("PATCH", "/v0/self/labels", JsonConvert.SerializeObject(new Dictionary<string, string> { [name] = value }), 200, ct);

		/// <summary>Removes one of this game server's labels.</summary>
		public Task<LocalReply> RemoveLabel(string name, CancellationToken ct = default) => SetLabel(name, null, ct);

		/// <summary>Frame times for the last second, once a second, for the tick graph. hz 0 leaves the rate out.</summary>
		public Task<LocalReply> Tick(double p50Ms, double p99Ms, int hz = 0, CancellationToken ct = default)
		{
			var body = new Dictionary<string, object> { ["p50_ms"] = p50Ms, ["p99_ms"] = p99Ms };
			if (hz > 0) body["hz"] = hz;
			return Send("POST", "/v0/self/tick", JsonConvert.SerializeObject(body), 204, ct);
		}

		private Task<LocalReply> ChangePlayers(string id, bool joined, CancellationToken ct)
		{
			lock (_gate)
			{
				var changed = joined ? _players.Add(id) : _players.Remove(id);
				if (!changed) return _reports;
				var ids = _players.ToArray();
				var previous = _reports;
				_reports = Next();
				return _reports;

				async Task<LocalReply> Next()
				{
					try { await previous; } catch { /* reported by the call that failed */ }
					return await Players(ids, ct);
				}
			}
		}

		private Task<LocalReply> Send(string method, string path, string json, int idleStatus, CancellationToken ct) =>
			Enabled ? _transport.Send(method, path, json, ct) : Task.FromResult(new LocalReply(idleStatus, ""));

		private static string Seconds(TimeSpan wait)
		{
			if (wait > MaxWait) wait = MaxWait;
			if (wait < TimeSpan.Zero) wait = TimeSpan.Zero;
			return ((int)Math.Round(wait.TotalSeconds)).ToString(System.Globalization.CultureInfo.InvariantCulture) + "s";
		}
	}
}
