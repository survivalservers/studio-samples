using System.Net;
using System.Text;
using System.Text.Json;
using GameServerAllocatorModule;
using Microsoft.Extensions.Logging.Abstractions;
using Moq;
using NUnit.Framework;
using Unity.Services.CloudCode.Apis;
using Unity.Services.CloudCode.Apis.Matchmaker;
using Unity.Services.CloudCode.Core;
using GameServerModule = GameServerAllocatorModule.GameServerAllocator;

namespace GameServerAllocator.Tests;

/// <summary>A fake of the API: answers from a queue and records every request with its body.</summary>
internal sealed class FakeApi : HttpMessageHandler
{
	public readonly List<(HttpRequestMessage Request, string Body)> Calls = new();
	private readonly Queue<Func<HttpResponseMessage>> _replies = new();

	public FakeApi Reply(HttpStatusCode status, object body, TimeSpan? retryAfter = null)
	{
		_replies.Enqueue(() =>
		{
			var res = new HttpResponseMessage(status) { Content = new StringContent(JsonSerializer.Serialize(body), Encoding.UTF8, "application/json") };
			if (retryAfter is { } wait) res.Headers.RetryAfter = new System.Net.Http.Headers.RetryConditionHeaderValue(wait);
			return res;
		});
		return this;
	}

	public FakeApi Fail()
	{
		_replies.Enqueue(() => throw new HttpRequestException("connection refused"));
		return this;
	}

	protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct)
	{
		var body = request.Content == null ? "" : await request.Content.ReadAsStringAsync(ct);
		Calls.Add((request, body));
		if (_replies.Count == 0) Assert.Fail($"unexpected call {request.Method} {request.RequestUri}");
		return _replies.Dequeue()();
	}
}

internal sealed class FakeHttp(HttpMessageHandler handler) : IHttpClientProvider
{
	private readonly HttpClient _client = new(handler);
	public HttpClient Get() => _client;
}

public class AllocatorTests
{
	private static readonly IExecutionContext Context = new Mock<IExecutionContext>().Object;

	private static object GameServer(string status, string? ip = "203.0.113.24", int port = 31042) => new
	{
		id = "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2",
		status,
		region = "us-central",
		address = new { ipv4 = ip, ipv6 = (string?)null, ports = new Dictionary<string, object> { ["game"] = new { port, protocol = "udp" } } },
	};

	private static object Problem(string code, string detail) => new { type = "about:blank", title = code, status = 0, code, detail, request_id = "req_1" };

	// Where these tests' requests go (a fake HTTP client answers them): a test always names its address.
	private const string TestApi = "https://api.test.invalid";

	[Test]
	public void ATestMustNameItsOwnAddress()
	{
		var refused = Assert.Throws<InvalidOperationException>(() => GameServerModule.BaseUrl(new AllocatorConfig { App = "brawl-server" }));
		Assert.That(refused!.Message, Does.Contain("tests must set one"));
		Assert.That(GameServerModule.BaseUrl(new AllocatorConfig { App = "brawl-server", ApiBaseUrl = TestApi }), Is.EqualTo(TestApi));
	}

	private static (GameServerModule Allocator, FakeApi Api, Mock<ISecretClient> Secrets) Make(AllocatorConfig? config = null, string key = "gsk_live_EXAMPLE_not_a_real_key")
	{
		var api = new FakeApi();
		var secrets = new Mock<ISecretClient>();
		secrets.Setup(s => s.GetSecret(It.IsAny<IExecutionContext>(), "GAME_SERVER_API_KEY")).ReturnsAsync(new Secret(key));
		config ??= new AllocatorConfig { App = "brawl-server" };
		config.ApiBaseUrl ??= TestApi; // a test names its address
		var allocator = new GameServerModule(secrets.Object, new FakeHttp(api), config, NullLogger<GameServerModule>.Instance)
		{
			Delay = (_, _) => Task.CompletedTask,
		};
		return (allocator, api, secrets);
	}

	private static AllocateRequest Match(string matchId = "9f1c2e7a-match", string pool = "ranked-eu", Dictionary<string, object>? props = null) =>
		new(matchId, new MatchmakingResults(null!, "pool_1", pool, "ranked", props ?? new Dictionary<string, object>()));

	private static JsonElement Body(FakeApi api, int call = 0) => JsonDocument.Parse(api.Calls[call].Body).RootElement;

	[Test]
	public async Task AllocateStartsAGameServerKeyedByTheMatch()
	{
		var (allocator, api, _) = Make();
		api.Reply(HttpStatusCode.Created, GameServer("starting"));

		var res = await allocator.Allocate(Context, Match());

		Assert.That(res.Status, Is.EqualTo(AllocateStatus.Created), res.Message);
		Assert.That(res.AllocationData["game_server_id"], Is.EqualTo("gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2"));
		Assert.That(res.AllocationData["region"], Is.EqualTo("us-central"));
		Assert.That(res.AllocationData.ContainsKey("host"), Is.False, "a starting game server has no joinable address yet");
		var (req, _) = api.Calls.Single();
		Assert.That(req.Method, Is.EqualTo(HttpMethod.Post));
		Assert.That(req.RequestUri!.ToString(), Is.EqualTo(TestApi + "/v0/game-servers"));
		Assert.That(req.Headers.Authorization!.ToString(), Is.EqualTo("Bearer gsk_live_EXAMPLE_not_a_real_key"));
		Assert.That(req.Headers.GetValues("Idempotency-Key").Single(), Is.EqualTo("9f1c2e7a-match"));
		var body = Body(api);
		Assert.That(body.GetProperty("app").GetString(), Is.EqualTo("brawl-server"));
		Assert.That(body.GetProperty("external_id").GetString(), Is.EqualTo("9f1c2e7a-match"));
		Assert.That(body.GetProperty("start_mode").GetString(), Is.EqualTo("auto"));
		Assert.That(body.GetProperty("labels").GetProperty("match_id").GetString(), Is.EqualTo("9f1c2e7a-match"));
		Assert.That(body.GetProperty("labels").GetProperty("pool").GetString(), Is.EqualTo("ranked-eu"));
		Assert.That(body.GetProperty("labels").GetProperty("queue").GetString(), Is.EqualTo("ranked"));
		Assert.That(body.GetProperty("env").GetProperty("MATCH_ID").GetString(), Is.EqualTo("9f1c2e7a-match"));
		Assert.That(body.TryGetProperty("placement", out _), Is.False, "no placement set: the app's default applies");
		Assert.That(body.TryGetProperty("version", out _) || body.TryGetProperty("branch", out _), Is.False, "the live branch by default");
	}

	[Test]
	public async Task AReadyServerIsAllocatedWithoutAnotherCall()
	{
		var (allocator, api, _) = Make();
		api.Reply(HttpStatusCode.Created, GameServer("running"));
		var res = await allocator.Allocate(Context, Match());
		Assert.That(res.AllocationData["host"], Is.EqualTo("203.0.113.24"));
		Assert.That(res.AllocationData["port"], Is.EqualTo(31042));

		var poll = await allocator.Poll(Context, new PollRequest("m", res.AllocationData, DateTimeOffset.UtcNow));
		Assert.That(poll.Status, Is.EqualTo(PollStatus.Allocated));
		Assert.That(poll.AssignmentData.Ip, Is.EqualTo("203.0.113.24"));
		Assert.That(poll.AssignmentData.Port, Is.EqualTo(31042));
		Assert.That(poll.AssignmentData.CustomData["game_server_id"], Is.EqualTo("gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2"));
		Assert.That(api.Calls, Has.Count.EqualTo(1), "the poll answered from the allocation data");
	}

	[Test]
	public async Task AColdStartIsPendingUntilRunningThenAllocated()
	{
		var (allocator, api, _) = Make();
		api.Reply(HttpStatusCode.OK, GameServer("starting")).Reply(HttpStatusCode.OK, GameServer("running"));
		var data = new Dictionary<string, object> { ["game_server_id"] = "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2" };

		var first = await allocator.Poll(Context, new PollRequest("m", data, DateTimeOffset.UtcNow));
		var second = await allocator.Poll(Context, new PollRequest("m", data, DateTimeOffset.UtcNow));

		Assert.That(first.Status, Is.EqualTo(PollStatus.Pending));
		Assert.That(second.Status, Is.EqualTo(PollStatus.Allocated));
		Assert.That(second.AssignmentData.Ip, Is.EqualTo("203.0.113.24"));
		Assert.That(api.Calls[0].Request.RequestUri!.ToString(), Is.EqualTo(TestApi + "/v0/game-servers/gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2"));
		Assert.That(api.Calls[0].Request.Method, Is.EqualTo(HttpMethod.Get));
	}

	[TestCase("failed")]
	[TestCase("stopped")]
	[TestCase("stopping")]
	public async Task AGameServerThatEndedIsAnError(string status)
	{
		var (allocator, api, _) = Make();
		api.Reply(HttpStatusCode.OK, GameServer(status));
		var poll = await allocator.Poll(Context, new PollRequest("m", new() { ["game_server_id"] = "gs_1x" }, DateTimeOffset.UtcNow));
		Assert.That(poll.Status, Is.EqualTo(PollStatus.Error));
		Assert.That(poll.Message, Does.Contain(status));
	}

	[Test]
	public async Task ARunningGameServerWithoutTheNamedPortIsAnError()
	{
		var (allocator, api, _) = Make(new AllocatorConfig { App = "brawl-server", PortName = "query" });
		api.Reply(HttpStatusCode.OK, GameServer("running"));
		var poll = await allocator.Poll(Context, new PollRequest("m", new() { ["game_server_id"] = "gs_1x" }, DateTimeOffset.UtcNow));
		Assert.That(poll.Status, Is.EqualTo(PollStatus.Error));
		Assert.That(poll.Message, Does.Contain("query"));
	}

	[Test]
	public async Task PollReadsAllocationDataThatCameBackAsJson()
	{
		// Unity stores the allocation data and returns it to each poll; numbers and strings can come back as JSON values.
		var (allocator, api, _) = Make();
		var stored = JsonSerializer.Deserialize<Dictionary<string, object>>("""{"game_server_id":"gs_1x","host":"203.0.113.9","port":31050}""")!;
		var poll = await allocator.Poll(Context, new PollRequest("m", stored, DateTimeOffset.UtcNow));
		Assert.That(poll.Status, Is.EqualTo(PollStatus.Allocated));
		Assert.That(poll.AssignmentData.Ip, Is.EqualTo("203.0.113.9"));
		Assert.That(poll.AssignmentData.Port, Is.EqualTo(31050));
		Assert.That(api.Calls, Is.Empty);
	}

	[Test]
	public async Task PlacementComesFromTheRegionThenThePoolThenTheDefault()
	{
		var config = new AllocatorConfig
		{
			App = "brawl-server",
			PlacementByRegion = { ["us-east"] = new PlacementChoice { Regions = { "us-east" }, Continents = { "north-america" } } },
			PlacementByPool = { ["ranked-eu"] = new PlacementChoice { Continents = { "europe" } } },
			DefaultPlacement = new PlacementChoice { Continents = { "north-america" } },
		};
		var (allocator, api, _) = Make(config);
		api.Reply(HttpStatusCode.Created, GameServer("starting")).Reply(HttpStatusCode.Created, GameServer("starting")).Reply(HttpStatusCode.Created, GameServer("starting"));

		await allocator.Allocate(Context, Match("m1", "ranked-eu", new() { ["Region"] = "us-east" }));
		await allocator.Allocate(Context, Match("m2", "ranked-eu", new() { ["Region"] = "somewhere-unmapped" }));
		await allocator.Allocate(Context, Match("m3", "casual"));

		Assert.That(Body(api, 0).GetProperty("placement").ToString(), Is.EqualTo("""{"regions":["us-east"],"continents":["north-america"]}"""));
		Assert.That(Body(api, 1).GetProperty("placement").ToString(), Is.EqualTo("""{"continents":["europe"]}"""));
		Assert.That(Body(api, 2).GetProperty("placement").ToString(), Is.EqualTo("""{"continents":["north-america"]}"""));
	}

	[Test]
	public async Task VersionBranchTimeLimitAndMatchPropertiesAreSent()
	{
		var config = new AllocatorConfig { App = "brawl-server", Branch = "beta", TimeLimit = TimeSpan.FromHours(2), StartMode = "cold" };
		var (allocator, api, _) = Make(config);
		api.Reply(HttpStatusCode.Created, GameServer("starting")).Reply(HttpStatusCode.Created, GameServer("starting"));
		var teams = new Dictionary<string, object> { ["Teams"] = new[] { new { TeamName = "red", PlayerIds = new[] { "p1", "p2" } } } };

		await allocator.Allocate(Context, Match(props: teams));
		config.Version = "1.4.2";
		await allocator.Allocate(Context, Match("m2"));

		var first = Body(api, 0);
		Assert.That(first.GetProperty("branch").GetString(), Is.EqualTo("beta"));
		Assert.That(first.GetProperty("ttl").GetString(), Is.EqualTo("7200s"));
		Assert.That(first.GetProperty("start_mode").GetString(), Is.EqualTo("cold"));
		Assert.That(first.GetProperty("env").GetProperty("MATCH_PROPERTIES").GetString(), Does.Contain("\"PlayerIds\":[\"p1\",\"p2\"]"));
		var second = Body(api, 1);
		Assert.That(second.GetProperty("version").GetString(), Is.EqualTo("1.4.2"));
		Assert.That(second.TryGetProperty("branch", out _), Is.False, "a version wins over a branch");
	}

	[Test]
	public async Task NoCapacityIsAnErrorWithTheReason()
	{
		var (allocator, api, _) = Make();
		api.Reply(HttpStatusCode.ServiceUnavailable, Problem("capacity_unavailable", "No capacity in europe."));
		var res = await allocator.Allocate(Context, Match());
		Assert.That(res.Status, Is.EqualTo(AllocateStatus.Error));
		Assert.That(res.Message, Is.EqualTo("capacity_unavailable: No capacity in europe."));
		Assert.That(api.Calls, Has.Count.EqualTo(1), "no capacity is not retried here; the ticket fails and Unity can match again");
	}

	[Test]
	public async Task RateLimitsOutagesAndAStartStillRunningAreRetriedWithTheSameKey()
	{
		var (allocator, api, _) = Make();
		api.Reply((HttpStatusCode)429, Problem("rate_limited", "slow down"), TimeSpan.FromSeconds(1))
			.Reply(HttpStatusCode.Conflict, Problem("idempotency_key_in_use", "the first call is still running"))
			.Reply(HttpStatusCode.Created, GameServer("running"));
		var res = await allocator.Allocate(Context, Match());
		Assert.That(res.Status, Is.EqualTo(AllocateStatus.Created), res.Message);
		Assert.That(api.Calls.Select(c => c.Request.Headers.GetValues("Idempotency-Key").Single()).Distinct().Single(), Is.EqualTo("9f1c2e7a-match"));
	}

	[Test]
	public async Task AFailedNetworkGivesUpAfterThreeTries()
	{
		var (allocator, api, _) = Make();
		api.Fail().Fail().Fail();
		var res = await allocator.Allocate(Context, Match());
		Assert.That(res.Status, Is.EqualTo(AllocateStatus.Error));
		Assert.That(res.Message, Does.StartWith("network:"));
		Assert.That(api.Calls, Has.Count.EqualTo(3));
	}

	[Test]
	public async Task APollThatCannotReachTheApiStaysPending()
	{
		var (allocator, api, _) = Make();
		api.Fail().Fail().Fail();
		var poll = await allocator.Poll(Context, new PollRequest("m", new() { ["game_server_id"] = "gs_1x" }, DateTimeOffset.UtcNow));
		Assert.That(poll.Status, Is.EqualTo(PollStatus.Pending), "Unity polls again until its own time limit");
	}

	[Test]
	public async Task MissingSetupFailsClearlyWithoutCallingTheApi()
	{
		var (noApp, api, _) = Make(new AllocatorConfig());
		Assert.That((await noApp.Allocate(Context, Match())).Message, Does.Contain("App"));

		var (noKey, api2, _) = Make(key: "");
		var res = await noKey.Allocate(Context, Match());
		Assert.That(res.Status, Is.EqualTo(AllocateStatus.Error));
		Assert.That(res.Message, Does.Contain("GAME_SERVER_API_KEY"));
		Assert.That(api.Calls.Concat(api2.Calls), Is.Empty);
	}

	[Test]
	public void TheFunctionsHaveTheNamesUnityMatchmakerCalls()
	{
		string? Name(string method) => typeof(GameServerModule).GetMethod(method)!.CustomAttributes
			.Single(a => a.AttributeType == typeof(CloudCodeFunctionAttribute)).ConstructorArguments[0].Value as string;
		Assert.That(Name("Allocate"), Is.EqualTo("Matchmaker_AllocateServer"));
		Assert.That(Name("Poll"), Is.EqualTo("Matchmaker_PollAllocation"));
	}
}
