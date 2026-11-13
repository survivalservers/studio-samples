using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;

namespace GameServerSample.Core
{
	/// <summary>
	/// One match: a UDP game port, the players in it, and the optional calls to the local API. The "game" is a
	/// tiny text protocol ("join name", "leave name", "ping"); replace it with your own netcode and keep the rest.
	///
	/// Single-threaded by design: call <see cref="Tick"/> often (every frame in Unity). The long-polls run in the
	/// background and hand their results to <see cref="Tick"/>, so game state is only touched there.
	/// </summary>
	public sealed class MatchServer : IDisposable
	{
		public const string ReadyLine = "Server ready for players";
		private static readonly TimeSpan LongPoll = TimeSpan.FromSeconds(30);

		private readonly GsConfig _cfg;
		private readonly LocalApi _api;
		private readonly Action<string> _log;
		private readonly TimeSpan? _matchLength;
		private readonly SortedSet<string> _players = new SortedSet<string>(StringComparer.Ordinal);
		private readonly ConcurrentQueue<Action> _inbox = new ConcurrentQueue<Action>();
		private CancellationTokenSource _loops;
		private UdpClient _udp;
		private DateTime _started;
		private int _peak;
		private bool _joined;

		public MatchServer(GsConfig cfg, LocalApi api, Action<string> log, TimeSpan? matchLength = null)
		{
			_cfg = cfg;
			_api = api;
			_log = log ?? (_ => { });
			_matchLength = matchLength;
		}

		/// <summary>The match data, once a match has picked this game server.</summary>
		public Allocation Allocation { get; private set; }

		/// <summary>Why the match ended; null while it runs.</summary>
		public string EndReason { get; private set; }

		/// <summary>True when the game server decided to end (and tells the platform), false when it was asked to stop.</summary>
		public bool SelfStop { get; private set; }

		public int PlayerCount => _players.Count;

		/// <summary>The bound game port (useful when ListenPort was 0).</summary>
		public int Port => ((IPEndPoint)_udp.Client.LocalEndPoint).Port;

		/// <summary>
		/// Binds the game port, then says ready, so a player is never sent to a port nobody listens on. Match data comes
		/// from the allocation call when there is a local API: it answers at once on a fresh start and is the only
		/// place secret settings come from on a ready server. Without one, the allocation file is read.
		/// </summary>
		public async Task StartAsync(CancellationToken ct)
		{
			_udp = new UdpClient(new IPEndPoint(IPAddress.Any, _cfg.ListenPort));
			_started = DateTime.UtcNow;
			_log($"listening on udp {Port}; players connect to {_cfg.PublicIp}:{_cfg.PublicPort}");
			if (!_api.Enabled) ReadAllocationFile();
			else
			{
				// A fresh start is allocated before it starts: read the match now, before saying ready, so it is set
				// up before the first player arrives. A ready server (pre-started) has none yet and waits after ready.
				var first = await _api.WaitAllocation(TimeSpan.Zero, ct);
				if (first != null) SetAllocation(first);
			}

			// A version can use this exact line as its ready check instead of the call below.
			_log(ReadyLine);
			await _api.Ready(ct);
			if (!_api.Enabled) return;

			_loops = CancellationTokenSource.CreateLinkedTokenSource(ct);
			if (Allocation == null) _ = AllocationLoop(_loops.Token);
			_ = StopLoop(_loops.Token);
		}

		/// <summary>Handles waiting packets and news from the long-polls. Cheap when there is nothing to do.</summary>
		public void Tick()
		{
			while (_inbox.TryDequeue(out var news)) news();
			while (EndReason == null && _udp != null && _udp.Available > 0)
			{
				var from = new IPEndPoint(IPAddress.Any, 0);
				var packet = _udp.Receive(ref from);
				var reply = Handle(Encoding.UTF8.GetString(packet).Trim(), out var changed);
				if (reply.Length > 0)
				{
					var bytes = Encoding.UTF8.GetBytes(reply);
					_udp.Send(bytes, bytes.Length, from);
				}
				if (!changed) continue;
				if (_joined && _players.Count == 0) End("match_over", selfStop: true);
			}
			if (EndReason == null && _matchLength is TimeSpan length && DateTime.UtcNow - _started >= length)
				End("match_over", selfStop: true);
		}

		/// <summary>For a stop signal the engine received (for example Ctrl+Break or SIGTERM): end without a self-stop.</summary>
		public void RequestEnd(string reason)
		{
			if (EndReason == null) End(reason, selfStop: false);
		}

		/// <summary>
		/// Finishes an ended match: the last player list goes out, the result file is written, and a self-stop tells
		/// the platform (the same as exiting with code 0). Then exit the process.
		/// </summary>
		public async Task FinishAsync(CancellationToken ct)
		{
			if (EndReason == null) End("stop_signal", selfStop: false);
			_loops?.Cancel();
			try
			{
				var reply = await _api.Reported;
				if (!reply.Ok) _log($"report players: {reply.Status}");
			}
			catch (Exception e) { _log("report players: " + e.Message); }
			WriteResult();
			if (SelfStop)
			{
				var reply = await _api.Stop(EndReason, ct);
				if (!reply.Ok) _log($"stop: {reply.Status} (exiting with 0 counts as a stop too)");
			}
		}

		/// <summary>
		/// For an engine that is already quitting and cannot wait for the network: ends the match and writes the result
		/// file, without the last player report or a self-stop.
		/// </summary>
		public void FinishNow(string reason)
		{
			RequestEnd(reason);
			_loops?.Cancel();
			WriteResult();
		}

		/// <summary>Runs one command from a player; says whether the player list changed.</summary>
		public string Handle(string line, out bool changed)
		{
			changed = false;
			var space = line.IndexOf(' ');
			var cmd = space < 0 ? line : line.Substring(0, space);
			var name = space < 0 ? "" : line.Substring(space + 1).Trim();
			if (cmd == "ping") return "pong";
			if (cmd == "join" && name.Length > 0)
			{
				// The platform refuses a player list holding such a name, so it never joins; the others keep counting.
				if (LocalApi.PlayerIdProblem(name) != null) return "refused: a name is 1 to 128 characters with no control characters";
				if (!_players.Add(name)) return "already in " + _cfg.Id;
				_joined = true;
				_peak = Math.Max(_peak, _players.Count);
				changed = true;
				_ = _api.PlayerConnect(name);
				_log($"{name} joined ({_players.Count} playing)");
				return $"welcome {name} to {_cfg.Id}";
			}
			if (cmd == "leave" && name.Length > 0)
			{
				if (!_players.Remove(name)) return "not in " + _cfg.Id;
				changed = true;
				_ = _api.PlayerDisconnect(name);
				_log($"{name} left ({_players.Count} playing)");
				return "bye " + name;
			}
			return "unknown command; try join <name>, leave <name> or ping";
		}

		public void Dispose()
		{
			_loops?.Cancel();
			_udp?.Dispose();
		}

		private void End(string reason, bool selfStop)
		{
			EndReason = reason;
			SelfStop = selfStop;
			_log($"match over: {reason} after {(int)(DateTime.UtcNow - _started).TotalSeconds}s, {_peak} players at most");
		}

		private async Task AllocationLoop(CancellationToken ct)
		{
			while (!ct.IsCancellationRequested)
			{
				try
				{
					var alloc = await _api.WaitAllocation(LongPoll, ct);
					if (alloc != null)
					{
						_inbox.Enqueue(() => SetAllocation(alloc));
						return;
					}
				}
				catch (Exception) when (!ct.IsCancellationRequested)
				{
					await Pause(ct);
				}
				catch (OperationCanceledException) { return; }
			}
		}

		private async Task StopLoop(CancellationToken ct)
		{
			while (!ct.IsCancellationRequested)
			{
				try
				{
					var notice = await _api.WaitShutdown(LongPoll, ct);
					if (notice != null)
					{
						_inbox.Enqueue(() =>
						{
							_log($"stop asked ({notice.Reason}); leaving before {notice.Deadline:O}");
							RequestEnd("stop_asked");
						});
						return;
					}
				}
				catch (Exception) when (!ct.IsCancellationRequested)
				{
					await Pause(ct);
				}
				catch (OperationCanceledException) { return; }
			}
		}

		private static async Task Pause(CancellationToken ct)
		{
			try { await Task.Delay(1000, ct); } catch (OperationCanceledException) { }
		}

		private void SetAllocation(Allocation alloc)
		{
			Allocation = alloc;
			_log($"allocated: match \"{alloc.ExternalId}\", labels {JsonConvert.SerializeObject(alloc.Labels)}, settings {string.Join(",", alloc.Env.Keys)}");
		}

		private void ReadAllocationFile()
		{
			if (string.IsNullOrEmpty(_cfg.AllocationFile) || !File.Exists(_cfg.AllocationFile)) return;
			try { SetAllocation(JsonConvert.DeserializeObject<Allocation>(File.ReadAllText(_cfg.AllocationFile))); }
			catch (Exception e) { _log("allocation file: " + e.Message); }
		}

		/// <summary>match-result.json in GS_ARTIFACT_DIR, collected after the game server exits.</summary>
		private void WriteResult()
		{
			if (string.IsNullOrEmpty(_cfg.ArtifactDir)) return;
			var result = new Dictionary<string, object>
			{
				["game_server_id"] = _cfg.Id,
				["reason"] = EndReason,
				["peak_players"] = _peak,
				["seconds"] = (int)(DateTime.UtcNow - _started).TotalSeconds,
			};
			if (Allocation != null) result["match"] = Allocation.ExternalId;
			try { File.WriteAllText(Path.Combine(_cfg.ArtifactDir, "match-result.json"), JsonConvert.SerializeObject(result, Formatting.Indented) + "\n"); }
			catch (Exception e) { _log("result file: " + e.Message); }
		}
	}
}
