using GameServerAllocatorModule;
using Microsoft.Extensions.Logging.Abstractions;
using Moq;
using NUnit.Framework;
using Unity.Services.CloudCode.Apis;
using Unity.Services.CloudCode.Apis.Matchmaker;
using Unity.Services.CloudCode.Core;
using GameServerModule = GameServerAllocatorModule.GameServerAllocator;

namespace GameServerAllocator.Tests;

/// <summary>
/// Against the practice server, which checks every request against the API description and answers with its
/// examples. Set PRACTICE_API (for example http://127.0.0.1:4010) to run; skipped otherwise.
/// </summary>
public class PracticeServerTests
{
	private sealed class RealHttp : IHttpClientProvider
	{
		private static readonly HttpClient Client = new();
		public HttpClient Get() => Client;
	}

	[Test]
	public async Task AllocateAndPollAreAcceptedByTheApiDescription()
	{
		var url = Environment.GetEnvironmentVariable("PRACTICE_API");
		if (string.IsNullOrEmpty(url)) Assert.Ignore("PRACTICE_API is not set");

		var secrets = new Mock<ISecretClient>();
		secrets.Setup(s => s.GetSecret(It.IsAny<IExecutionContext>(), It.IsAny<string>())).ReturnsAsync(new Secret("gsk_live_EXAMPLE_not_a_real_key"));
		var config = new AllocatorConfig
		{
			ApiBaseUrl = url!,
			App = "brawl-server",
			Branch = "live",
			TimeLimit = TimeSpan.FromHours(2),
			PlacementByRegion = { ["us-east"] = new PlacementChoice { Regions = { "us-east" }, Continents = { "north-america" } } },
		};
		var allocator = new GameServerModule(secrets.Object, new RealHttp(), config, NullLogger<GameServerModule>.Instance);
		var context = new Mock<IExecutionContext>().Object;
		var props = new Dictionary<string, object> { ["Region"] = "us-east", ["Teams"] = new[] { new { PlayerIds = new[] { "p1" } } } };

		var res = await allocator.Allocate(context, new AllocateRequest(Guid.NewGuid().ToString(), new MatchmakingResults(null!, "pool_1", "pool", "queue", props)));
		Assert.That(res.Status, Is.EqualTo(AllocateStatus.Created), res.Message);

		var poll = await allocator.Poll(context, new PollRequest("m", new() { ["game_server_id"] = res.AllocationData["game_server_id"] }, DateTimeOffset.UtcNow));
		Assert.That(poll.Status, Is.Not.EqualTo(PollStatus.Error), poll.Message);
	}
}
