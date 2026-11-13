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
using GameServerSample.Core;
using Newtonsoft.Json;
using NUnit.Framework;

namespace GameServerSample.Tests
{
	/// <summary>
	/// The local API in memory: records calls in order, answers the allocation long-poll on its second call (as
	/// for a ready server) or its first (Cold, as for a fresh start for a match), and the shutdown
	/// long-poll once the test asks.
	/// </summary>
	internal sealed class FakeAgent : ILocalTransport
	{
		public readonly ConcurrentQueue<string> Calls = new ConcurrentQueue<string>();
		public Allocation Alloc;
		public readonly TaskCompletionSource<bool> StopAsked = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
		public int Polls;
		public bool Cold;
		public int ReadyStatus = 204;

		public async Task<LocalReply> Send(string method, string path, string json, CancellationToken ct)
		{
			var route = method + " " + path.Split('?')[0];
			switch (route)
			{
				case "POST /v0/self/ready":
					Calls.Enqueue(route);
					return new LocalReply(ReadyStatus, "");
				case "GET /v0/self/allocation":
					Assert.That(path, Does.Contain("?wait=30s").Or.Contain("?wait=0s"));
					if ((Interlocked.Increment(ref Polls) == 1 && !Cold) || Alloc == null)
					{
						await Task.Delay(50, ct);
						return new LocalReply(204, "");
					}
					Calls.Enqueue("GET allocation");
					return new LocalReply(200, JsonConvert.SerializeObject(Alloc));
				case "GET /v0/self/shutdown":
					var done = await Task.WhenAny(StopAsked.Task, Task.Delay(200, ct));
					if (done != StopAsked.Task) return new LocalReply(204, "");
					Calls.Enqueue("GET shutdown");
					return new LocalReply(200, "{\"requested_at\":\"2026-10-19T19:59:30Z\",\"deadline\":\"2026-10-19T20:00:00Z\",\"reason\":\"api\"}");
				case "PUT /v0/self/players":
					var players = JsonConvert.DeserializeAnonymousType(json, new { ids = (string[])null, count = (int?)null });
					Calls.Enqueue(players.ids != null ? "PUT players " + string.Join(",", players.ids) : "PUT count " + players.count);
					return new LocalReply(200, "");
				case "PATCH /v0/self/labels":
				case "POST /v0/self/tick":
				case "POST /v0/self/health":
					Calls.Enqueue(route + " " + json);
					return new LocalReply(route == "PATCH /v0/self/labels" ? 200 : 204, "");
				case "POST /v0/self/stop":
					Calls.Enqueue("POST stop " + JsonConvert.DeserializeAnonymousType(json, new { reason = "" }).reason);
					return new LocalReply(202, "");
				default:
					Assert.Fail("unexpected " + route);
					return default;
			}
		}
	}

	public class MatchServerTests
	{
		private readonly List<string> _log = new List<string>();
		private string Log => string.Join("\n", _log);

		private MatchServer Make(FakeAgent agent, GsConfig cfg = null, TimeSpan? length = null)
		{
			cfg = cfg ?? new GsConfig { Id = "gs_1", ListenPort = 0 };
			cfg.ListenPort = 0;
			return new MatchServer(cfg, new LocalApi(agent), line => { lock (_log) _log.Add(line); }, length);
		}

		// Runs the server's Tick loop (as Unity's Update would) until the match ends or the time runs out.
		private static async Task Run(MatchServer server, Func<bool> until = null, int ms = 5000)
		{
			var end = DateTime.UtcNow.AddMilliseconds(ms);
			while (DateTime.UtcNow < end)
			{
				server.Tick();
				if (until != null ? until() : server.EndReason != null) return;
				await Task.Delay(10);
			}
			Assert.Fail("timed out");
		}

		private static async Task<string> Player(MatchServer server, string cmd)
		{
			using (var udp = new UdpClient())
			{
				var bytes = Encoding.UTF8.GetBytes(cmd);
				await udp.SendAsync(bytes, bytes.Length, new IPEndPoint(IPAddress.Loopback, server.Port));
				var reply = udp.ReceiveAsync();
				var deadline = DateTime.UtcNow.AddSeconds(3);
				while (!reply.IsCompleted && DateTime.UtcNow < deadline)
				{
					server.Tick();
					await Task.Delay(5);
				}
				Assert.That(reply.IsCompleted, Is.True, "no reply to " + cmd);
				return Encoding.UTF8.GetString(reply.Result.Buffer);
			}
		}

		[Test]
		public void ATransportWaitsLongerThanTheLongestLongPoll()
		{
			// Both transports (HttpClientTransport and UnityWebRequestTransport) wait this long for an answer: with less
			// than MaxWait a 60 s long-poll was cut off at 40 s and read as a failure.
			Assert.That(LocalApi.TransportTimeout, Is.GreaterThan(LocalApi.MaxWait));
			using (var t = new HttpClientTransport("http://127.0.0.1:1", "token"))
				Assert.That(t.Timeout, Is.EqualTo(LocalApi.TransportTimeout));
		}

		[Test]
		public async Task AColdStartReadsItsMatchBeforeSayingReady()
		{
			var agent = new FakeAgent { Cold = true, Alloc = new Allocation { GameServerId = "gs_1", ExternalId = "cold-1" } };
			using (var server = Make(agent))
			{
				await server.StartAsync(CancellationToken.None);
				Assert.That(server.Allocation?.ExternalId, Is.EqualTo("cold-1"), "set up before the first player can arrive");
				await Task.Delay(200);
				Assert.That(agent.Calls.Take(2), Is.EqualTo(new[] { "GET allocation", "POST /v0/self/ready" }));
				Assert.That(agent.Polls, Is.EqualTo(1), "no second wait once the match is known");
				Assert.That(Log, Does.Contain("allocated: match \"cold-1\""));
			}
		}

		[Test]
		public async Task AWarmMatchFromReadyToSelfStop()
		{
			var agent = new FakeAgent { Alloc = new Allocation { GameServerId = "gs_1", ExternalId = "88231", Env = { ["MATCH_MODE"] = "ranked" } } };
			var artifacts = Directory.CreateTempSubdirectory().FullName;
			using (var server = Make(agent, new GsConfig { Id = "gs_1", ArtifactDir = artifacts }))
			{
				await server.StartAsync(CancellationToken.None);
				await Run(server, () => server.Allocation != null);

				Assert.That(await Player(server, "join ana"), Is.EqualTo("welcome ana to gs_1"));
				await Player(server, "join bo");
				Assert.That(await Player(server, "ping"), Is.EqualTo("pong"));
				await Player(server, "leave ana");
				await Player(server, "leave bo");
				Assert.That(server.EndReason, Is.EqualTo("match_over"));
				await server.FinishAsync(CancellationToken.None);
			}

			var calls = agent.Calls.ToArray();
			Assert.That(calls.First(), Is.EqualTo("POST /v0/self/ready"));
			Assert.That(calls.Last(), Is.EqualTo("POST stop match_over"));
			Assert.That(calls[calls.Length - 2], Is.EqualTo("PUT players "), "the empty list goes out before the stop");
			Assert.That(calls, Does.Contain("PUT players ana,bo"));
			Assert.That(Log, Does.Contain(MatchServer.ReadyLine));
			Assert.That(Log, Does.Contain("allocated: match \"88231\""));
			var result = JsonConvert.DeserializeObject<Dictionary<string, object>>(File.ReadAllText(Path.Combine(artifacts, "match-result.json")));
			Assert.That(result["reason"], Is.EqualTo("match_over"));
			Assert.That(result["peak_players"], Is.EqualTo(2));
			Assert.That(result["match"], Is.EqualTo("88231"));
		}

		[Test]
		public async Task AStopNoticeEndsTheMatchWithoutASelfStop()
		{
			var agent = new FakeAgent { Alloc = new Allocation { GameServerId = "gs_1" } };
			using (var server = Make(agent))
			{
				await server.StartAsync(CancellationToken.None);
				await Player(server, "join cy");
				agent.StopAsked.SetResult(true);
				await Run(server);
				Assert.That(server.EndReason, Is.EqualTo("stop_asked"));
				Assert.That(server.SelfStop, Is.False);
				await server.FinishAsync(CancellationToken.None);
			}
			Assert.That(agent.Calls.Any(c => c.StartsWith("POST stop")), Is.False);
		}

		[Test]
		public async Task WithNoLocalAPIItRunsLocallyAndReadsTheAllocationFile()
		{
			var file = Path.Combine(Directory.CreateTempSubdirectory().FullName, "allocation.json");
			File.WriteAllText(file, "{\"game_server_id\":\"gs_2\",\"external_id\":\"m-file\",\"labels\":{\"queue\":\"duo\"}}");
			using (var server = new MatchServer(new GsConfig { ListenPort = 0, AllocationFile = file }, new LocalApi(null), line => _log.Add(line), TimeSpan.FromMilliseconds(200)))
			{
				await server.StartAsync(CancellationToken.None);
				Assert.That(server.Allocation.ExternalId, Is.EqualTo("m-file"));
				Assert.That(await Player(server, "ping"), Is.EqualTo("pong"));
				await Run(server);
				Assert.That(server.EndReason, Is.EqualTo("match_over"), "the match length ran out");
				await server.FinishAsync(CancellationToken.None);
			}
		}

		[Test]
		public void AFailedReadyCallStopsTheStart()
		{
			var agent = new FakeAgent { ReadyStatus = 401 };
			using (var server = Make(agent))
			{
				var e = Assert.ThrowsAsync<InvalidOperationException>(() => server.StartAsync(CancellationToken.None));
				Assert.That(e.Message, Does.Contain("401"));
			}
		}

		[Test]
		public async Task TheCallsHaveTheSdkNamesAndShapes()
		{
			var agent = new FakeAgent();
			var api = new LocalApi(agent);
			await api.PlayerConnect("p_2");
			await api.PlayerConnect("p_1");
			await api.PlayerConnect("p_1"); // already in: nothing sent
			await api.PlayerDisconnect("p_2");
			await api.SetPlayerCount(3);
			await api.SetLabel("map", "desert");
			await api.RemoveLabel("queue");
			await api.Tick(2.1, 3.9, 128);
			await api.Tick(2.5, 6.0);
			await api.Health();
			Assert.That(agent.Calls.ToArray(), Is.EqualTo(new[]
			{
				"PUT players p_2", "PUT players p_1,p_2", "PUT players p_1", "PUT count 3",
				"PATCH /v0/self/labels {\"map\":\"desert\"}", "PATCH /v0/self/labels {\"queue\":null}",
				"POST /v0/self/tick {\"p50_ms\":2.1,\"p99_ms\":3.9,\"hz\":128}", "POST /v0/self/tick {\"p50_ms\":2.5,\"p99_ms\":6.0}",
				"POST /v0/self/health ",
			}));
			Assert.That(api.ConnectedPlayers, Is.EqualTo(new[] { "p_1" }));

			agent.StopAsked.SetResult(true);
			var shutdown = await api.WaitShutdown(TimeSpan.FromMinutes(5));
			Assert.That((shutdown.Reason, shutdown.Deadline), Is.EqualTo(("api", new DateTime(2026, 10, 19, 20, 0, 0, DateTimeKind.Utc))));
		}

		[Test]
		public async Task WaitsAreCappedAtSixtySecondsAndNoAgentMeansNothingIsSent()
		{
			var seen = new List<string>();
			var api = new LocalApi(new PathRecorder(seen));
			await api.WaitAllocation(TimeSpan.FromMinutes(5));
			await api.WaitShutdown(TimeSpan.FromSeconds(30));
			Assert.That(seen, Is.EqualTo(new[] { "/v0/self/allocation?wait=60s", "/v0/self/shutdown?wait=30s" }));

			var idle = new LocalApi(null);
			await idle.Ready();
			Assert.That(await idle.WaitAllocation(TimeSpan.FromSeconds(30)), Is.Null);
			Assert.That(await idle.WaitShutdown(TimeSpan.FromSeconds(30)), Is.Null);
			Assert.That((await idle.PlayerConnect("p_1")).Ok, Is.True);
			Assert.That((await idle.SetLabel("map", "desert")).Ok, Is.True);
			Assert.That((await idle.Tick(1, 2, 60)).Ok, Is.True);
			Assert.That(idle.ConnectedPlayers, Is.EqualTo(new[] { "p_1" }), "the list is still kept");
		}

		private sealed class PathRecorder : ILocalTransport
		{
			private readonly List<string> _seen;
			public PathRecorder(List<string> seen) => _seen = seen;

			public Task<LocalReply> Send(string method, string path, string json, CancellationToken ct)
			{
				_seen.Add(path);
				return Task.FromResult(new LocalReply(204, ""));
			}
		}

		[Test]
		public void TheGameProtocol()
		{
			using (var server = Make(new FakeAgent()))
			{
				Assert.That(server.Handle("join ana", out var changed), Is.EqualTo("welcome ana to gs_1"));
				Assert.That(changed, Is.True);
				Assert.That(server.Handle("join ana", out changed), Is.EqualTo("already in gs_1"));
				Assert.That(changed, Is.False);
				Assert.That(server.Handle("leave bo", out changed), Is.EqualTo("not in gs_1"));
				Assert.That(server.Handle("dance", out changed), Does.StartWith("unknown command"));
				// A name a player chose, with a NUL in it: refused, and ana still counts.
				Assert.That(server.Handle("join bad\0name", out changed), Does.StartWith("refused"));
				Assert.That(changed, Is.False);
				Assert.That(server.PlayerCount, Is.EqualTo(1));
			}
		}

		[Test]
		public async Task APlayerIdWithAControlCharacterNeverJoinsTheList()
		{
			var agent = new FakeAgent();
			var api = new LocalApi(agent);
			await api.PlayerConnect("ana");
			var refused = await api.PlayerConnect("bad\u0001name");
			Assert.That(refused.Status, Is.EqualTo(422));
			Assert.That(api.ConnectedPlayers, Is.EquivalentTo(new[] { "ana" }));
			Assert.That(LocalApi.PlayerIdProblem("Zoë 🎮"), Is.Null);
			Assert.That(LocalApi.PlayerIdProblem(new string('é', 129)), Is.EqualTo("is longer than 128 characters"));
			Assert.That(LocalApi.PlayerIdProblem("del\u007f"), Is.EqualTo("has a control character"));
		}

		[Test]
		public void ConfigFromTheEnvironment()
		{
			Func<string, string> env(Dictionary<string, string> m) => k => m.TryGetValue(k, out var v) ? v : null;

			var local = GsConfig.From(env(new Dictionary<string, string>()));
			Assert.That((local.Id, local.ListenPort, local.AgentUrl), Is.EqualTo(("local", 7777, "")));
			var windows = GsConfig.From(env(new Dictionary<string, string> { ["GS_PORT_GAME"] = "31042", ["GS_LOCAL_API_URL"] = "http://127.0.0.1:7600/" }));
			Assert.That((windows.ListenPort, windows.PublicPort, windows.AgentUrl), Is.EqualTo((31042, 31042, "http://127.0.0.1:7600")));
			var linux = GsConfig.From(env(new Dictionary<string, string> { ["GS_PORT_GAME"] = "31042", ["GS_PORT_GAME_INTERNAL"] = "7777" }));
			Assert.That((linux.ListenPort, linux.PublicPort), Is.EqualTo((7777, 31042)));
			Assert.Throws<FormatException>(() => GsConfig.From(env(new Dictionary<string, string> { ["GS_PORT_GAME"] = "seven" })));
		}

		[Test]
		public void OlderHostNamesChangeNothing()
		{
			// The older names (GS_KIND=world, and the local API's address in GS_AGENT_URL instead of GS_LOCAL_API_URL)
			// give the same settings as today's.
			var common = new Dictionary<string, string> { ["GS_ID"] = "gs_1", ["GS_PORT_GAME"] = "31042", ["GS_TOKEN"] = "t" };
			Func<string, string> Env(params (string Name, string Value)[] names)
			{
				var all = new Dictionary<string, string>(common);
				foreach (var n in names) all[n.Name] = n.Value;
				return name => all.TryGetValue(name, out var value) ? value : null;
			}
			var current = GsConfig.From(Env(("GS_KIND", "persistent_server"), ("GS_LOCAL_API_URL", "http://127.0.0.1:7600")));
			var older = GsConfig.From(Env(("GS_KIND", "world"), ("GS_AGENT_URL", "http://127.0.0.1:7600")));
			Assert.That(JsonConvert.SerializeObject(older), Is.EqualTo(JsonConvert.SerializeObject(current)));
			Assert.That((older.Id, older.ListenPort, older.AgentUrl, older.Token), Is.EqualTo(("gs_1", 31042, "http://127.0.0.1:7600", "t")));
		}

		[Test]
		public async Task TheHttpTransportSendsTheTokenAndBody()
		{
			var handler = new RecordingHandler();
			using (var transport = new HttpClientTransport("http://127.0.0.1:7600/", "tok", handler))
			{
				var reply = await new LocalApi(transport).Players(new[] { "p_1", "p_2" }, CancellationToken.None);
				Assert.That(reply.Status, Is.EqualTo(200));
			}
			Assert.That(handler.Seen, Is.EqualTo("PUT http://127.0.0.1:7600/v0/self/players Bearer tok {\"ids\":[\"p_1\",\"p_2\"]}"));
		}

		private sealed class RecordingHandler : System.Net.Http.HttpMessageHandler
		{
			public string Seen;

			protected override async Task<System.Net.Http.HttpResponseMessage> SendAsync(System.Net.Http.HttpRequestMessage r, CancellationToken ct)
			{
				Seen = $"{r.Method} {r.RequestUri} {r.Headers.Authorization} {await r.Content.ReadAsStringAsync(ct)}";
				return new System.Net.Http.HttpResponseMessage(HttpStatusCode.OK) { Content = new System.Net.Http.StringContent("{}") };
			}
		}
	}
}
