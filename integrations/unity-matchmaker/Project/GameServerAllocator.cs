using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using GameServerAllocatorModule.Api;
using Microsoft.Extensions.Logging;
using Unity.Services.CloudCode.Apis;
using Unity.Services.CloudCode.Apis.Matchmaker;
using Unity.Services.CloudCode.Core;

namespace GameServerAllocatorModule;

/// <summary>
/// Unity Matchmaker's allocator: for each match it starts a game server through the API and hands Unity the address.
/// Deploy the module, then in the queue's hosting settings pick this module with <c>Matchmaker_AllocateServer</c> as
/// the allocate function and <c>Matchmaker_PollAllocation</c> as the poll function.
/// </summary>
public class GameServerAllocator(
	ISecretClient secrets,
	IHttpClientProvider httpClients,
	AllocatorConfig config,
	ILogger<GameServerAllocator> logger) : IMatchmakerAllocator
{
	// Keys in Unity's allocation data, which Unity hands back to every poll.
	internal const string GameServerIdKey = "game_server_id";
	internal const string HostKey = "host";
	internal const string PortKey = "port";
	internal const string RegionKey = "region";

	private const int MaxEnvValue = 32768;

	internal Func<TimeSpan, System.Threading.CancellationToken, Task>? Delay { get; set; }

	[CloudCodeFunction("Matchmaker_AllocateServer")]
	public async Task<AllocateResponse> Allocate(IExecutionContext context, AllocateRequest request)
	{
		if (string.IsNullOrWhiteSpace(config.App))
			return AllocateError("AllocatorConfig.App is not set in ModuleConfig");
		try
		{
			var api = await Api(context);
			var start = BuildStart(request);
			// The match ID as the Idempotency-Key: if Unity calls again for the same match, the API returns the game
			// server it already started instead of starting another.
			var result = await api.StartAsync(start, request.MatchId);
			if (!result.Ok)
			{
				logger.LogError("Starting a game server for match {MatchId} failed ({Status}): {Problem}", request.MatchId, (int)result.Status, result.Problem);
				return AllocateError(result.Problem!.ToString());
			}

			var gs = result.GameServer!;
			var data = new Dictionary<string, object> { [GameServerIdKey] = gs.Id };
			if (!string.IsNullOrEmpty(gs.Region)) data[RegionKey] = gs.Region!;
			// A ready server is joinable at once, so the first poll can answer without another call.
			if (gs.Status == "running" && gs.Endpoint(config.PortName) is { } ep)
			{
				data[HostKey] = ep.Host;
				data[PortKey] = ep.Port;
			}
			logger.LogInformation("Match {MatchId}: game server {GameServerId} in {Region}, {Status}", request.MatchId, gs.Id, gs.Region, gs.Status);
			return new AllocateResponse(AllocateStatus.Created) { AllocationData = data };
		}
		catch (Exception e)
		{
			logger.LogError(e, "Starting a game server for match {MatchId} failed", request.MatchId);
			return AllocateError(e.Message);
		}
	}

	[CloudCodeFunction("Matchmaker_PollAllocation")]
	public async Task<PollResponse> Poll(IExecutionContext context, PollRequest request)
	{
		var data = request.AllocationData ?? new Dictionary<string, object>();
		if (Text(data, HostKey) is { Length: > 0 } host && Number(data, PortKey) is > 0 and var port)
			return Allocated(host, port, data);

		var id = Text(data, GameServerIdKey);
		if (string.IsNullOrEmpty(id))
			return new PollResponse(PollStatus.Error) { Message = "no game server ID in the allocation data" };
		try
		{
			var api = await Api(context);
			var result = await api.GetAsync(id);
			if (!result.Ok)
			{
				// Rate limits and brief outages: Unity polls again.
				if (result.Transient) return new PollResponse(PollStatus.Pending);
				return new PollResponse(PollStatus.Error) { Message = result.Problem!.ToString() };
			}

			var gs = result.GameServer!;
			switch (gs.Status)
			{
				case "starting":
					return new PollResponse(PollStatus.Pending);
				case "running":
					if (gs.Endpoint(config.PortName) is { } ep) return Allocated(ep.Host, ep.Port, data);
					return new PollResponse(PollStatus.Error) { Message = $"game server {id} has no IPv4 address or no port named \"{config.PortName}\"" };
				default: // stopping, stopped, failed
					return new PollResponse(PollStatus.Error) { Message = $"game server {id} is {gs.Status}" };
			}
		}
		catch (Exception e)
		{
			logger.LogError(e, "Polling game server {GameServerId} failed", id);
			return new PollResponse(PollStatus.Pending);
		}
	}

	private async Task<GameServersApi> Api(IExecutionContext context)
	{
		var secret = await secrets.GetSecret(context, config.ApiKeySecretName);
		if (string.IsNullOrEmpty(secret?.Value))
			throw new InvalidOperationException($"the Unity secret {config.ApiKeySecretName} is empty");
		return new GameServersApi(httpClients.Get(), BaseUrl(config), secret.Value, Delay);
	}

	/// <summary>The address to call: the config's, else the default, which a test host may not use.</summary>
	internal static string BaseUrl(AllocatorConfig config)
	{
		if (!string.IsNullOrEmpty(config.ApiBaseUrl)) return config.ApiBaseUrl;
		if (UnderTest()) throw new InvalidOperationException("no API address given: tests must set one");
		return AllocatorConfig.DefaultApiBaseUrl;
	}

	private static bool UnderTest()
	{
		if (Environment.GetEnvironmentVariable("GS_API_URL_REQUIRED") == "1") return true;
		foreach (var a in AppDomain.CurrentDomain.GetAssemblies())
		{
			var name = a.GetName().Name;
			if (name is "nunit.framework" or "xunit.core" or "Microsoft.VisualStudio.TestPlatform.TestFramework" or "testhost") return true;
		}
		return false;
	}

	internal StartRequest BuildStart(AllocateRequest request)
	{
		var results = request.MatchmakingResults;
		var start = new StartRequest
		{
			App = config.App,
			Version = string.IsNullOrWhiteSpace(config.Version) ? null : config.Version,
			Branch = string.IsNullOrWhiteSpace(config.Version) && !string.IsNullOrWhiteSpace(config.Branch) ? config.Branch : null,
			StartMode = string.IsNullOrWhiteSpace(config.StartMode) ? null : config.StartMode,
			ExternalId = request.MatchId,
			Ttl = config.TimeLimit is { } limit ? $"{(long)limit.TotalSeconds}s" : null,
			Labels = new Dictionary<string, string> { ["matchmaker"] = "unity", ["match_id"] = Label(request.MatchId) },
		};
		if (!string.IsNullOrEmpty(results?.PoolName)) start.Labels["pool"] = Label(results.PoolName);
		if (!string.IsNullOrEmpty(results?.QueueName)) start.Labels["queue"] = Label(results.QueueName);

		var placement = ResolvePlacement(results);
		if (placement is { IsEmpty: false })
		{
			start.Placement = new PlacementBody
			{
				Regions = placement.Regions.Count > 0 ? placement.Regions.ToList() : null,
				Continents = placement.Continents.Count > 0 ? placement.Continents.ToList() : null,
			};
		}

		start.Env = new Dictionary<string, string> { ["MATCH_ID"] = request.MatchId };
		if (config.SendMatchProperties && results?.MatchProperties is { Count: > 0 } props)
		{
			var json = JsonSerializer.Serialize(props);
			if (json.Length <= MaxEnvValue) start.Env["MATCH_PROPERTIES"] = json;
			else logger.LogWarning("Match {MatchId}: match properties are {Length} characters, over the {Max} a setting can hold; not sent", request.MatchId, json.Length, MaxEnvValue);
		}
		return start;
	}

	/// <summary>The match's Region property first, then the pool name, then the default.</summary>
	internal PlacementChoice? ResolvePlacement(MatchmakingResults? results)
	{
		if (results?.MatchProperties != null && results.MatchProperties.TryGetValue("Region", out var regionValue))
		{
			var region = regionValue?.ToString();
			if (!string.IsNullOrEmpty(region))
			{
				if (config.PlacementByRegion.TryGetValue(region, out var byRegion)) return byRegion;
				logger.LogWarning("The match's Region {Region} is not in PlacementByRegion", region);
			}
		}
		if (!string.IsNullOrEmpty(results?.PoolName) && config.PlacementByPool.TryGetValue(results.PoolName, out var byPool))
			return byPool;
		return config.DefaultPlacement;
	}

	private static string Label(string value) => value.Length <= 256 ? value : value[..256];

	private static PollResponse Allocated(string host, int port, Dictionary<string, object> data)
	{
		var custom = new Dictionary<string, object>();
		if (Text(data, GameServerIdKey) is { Length: > 0 } id) custom[GameServerIdKey] = id;
		return new PollResponse(PollStatus.Allocated) { AssignmentData = AssignmentData.IpPort(host, port, custom) };
	}

	private static AllocateResponse AllocateError(string message) => new(AllocateStatus.Error) { Message = message };

	// Unity stores the allocation data and hands it back to each poll; values may come back as strings, numbers or
	// JSON values, so read them loosely.
	internal static string? Text(Dictionary<string, object> data, string key) =>
		data.TryGetValue(key, out var v) && v != null ? (v is JsonElement { ValueKind: JsonValueKind.String } je ? je.GetString() : v.ToString()) : null;

	internal static int Number(Dictionary<string, object> data, string key) =>
		int.TryParse(Text(data, key), NumberStyles.Integer, CultureInfo.InvariantCulture, out var n) ? n : 0;
}
