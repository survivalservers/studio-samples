using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Threading;
using System.Threading.Tasks;

namespace GameServerAllocatorModule.Api;

/// <summary>The start request (<c>POST /v0/game-servers</c>); only the fields the allocator sends.</summary>
public sealed class StartRequest
{
	[JsonPropertyName("app")] public string App { get; set; } = "";
	[JsonPropertyName("version")] public string? Version { get; set; }
	[JsonPropertyName("branch")] public string? Branch { get; set; }
	[JsonPropertyName("placement")] public PlacementBody? Placement { get; set; }
	[JsonPropertyName("start_mode")] public string? StartMode { get; set; }
	[JsonPropertyName("env")] public Dictionary<string, string>? Env { get; set; }
	[JsonPropertyName("labels")] public Dictionary<string, string>? Labels { get; set; }
	[JsonPropertyName("external_id")] public string? ExternalId { get; set; }
	[JsonPropertyName("ttl")] public string? Ttl { get; set; }
}

public sealed class PlacementBody
{
	[JsonPropertyName("regions")] public List<string>? Regions { get; set; }
	[JsonPropertyName("continents")] public List<string>? Continents { get; set; }
}

/// <summary>The parts of a game server the allocator reads.</summary>
public sealed class GameServer
{
	[JsonPropertyName("id")] public string Id { get; set; } = "";
	[JsonPropertyName("status")] public string Status { get; set; } = "";
	[JsonPropertyName("region")] public string? Region { get; set; }
	[JsonPropertyName("address")] public Address? Address { get; set; }

	/// <summary>The public IPv4 address and the port with that name, or null when either is missing.</summary>
	public (string Host, int Port)? Endpoint(string portName)
	{
		if (string.IsNullOrEmpty(Address?.Ipv4) || Address.Ports == null) return null;
		if (!Address.Ports.TryGetValue(portName, out var binding) || binding.Port <= 0) return null;
		return (Address.Ipv4!, binding.Port);
	}
}

public sealed class Address
{
	[JsonPropertyName("ipv4")] public string? Ipv4 { get; set; }
	[JsonPropertyName("ports")] public Dictionary<string, PortBinding>? Ports { get; set; }
}

public sealed class PortBinding
{
	[JsonPropertyName("port")] public int Port { get; set; }
	[JsonPropertyName("protocol")] public string? Protocol { get; set; }
}

/// <summary>An error reply (RFC 9457 problem details with a stable code).</summary>
public sealed class Problem
{
	[JsonPropertyName("code")] public string? Code { get; set; }
	[JsonPropertyName("title")] public string? Title { get; set; }
	[JsonPropertyName("detail")] public string? Detail { get; set; }

	public override string ToString() => $"{Code ?? "error"}: {Detail ?? Title ?? "no detail"}";
}

/// <summary>The outcome of one call: the game server, or the HTTP status and the problem.</summary>
public sealed class ApiResult
{
	public HttpStatusCode Status { get; init; }
	public GameServer? GameServer { get; init; }
	public Problem? Problem { get; init; }
	public bool Ok => GameServer != null;

	/// <summary>Worth asking again later: rate limits, our own errors and a brief outage. A network failure has status 0.</summary>
	public bool Transient => Status == 0 || Status == (HttpStatusCode)429 || ((int)Status >= 500 && Problem?.Code != "capacity_unavailable");
}

/// <summary>
/// The two calls the allocator makes. A start is retried with the same Idempotency-Key, so a retry after a lost
/// reply returns the first game server instead of starting a second one.
/// </summary>
public sealed class GameServersApi
{
	private static readonly JsonSerializerOptions Json = new() { DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull };

	private readonly HttpClient _http;
	private readonly string _baseUrl;
	private readonly string _apiKey;
	private readonly Func<TimeSpan, CancellationToken, Task> _delay;

	/// <param name="delay">How to wait between attempts; tests pass one that does not wait.</param>
	public GameServersApi(HttpClient http, string baseUrl, string apiKey, Func<TimeSpan, CancellationToken, Task>? delay = null)
	{
		_http = http;
		_baseUrl = baseUrl.TrimEnd('/');
		_apiKey = apiKey;
		_delay = delay ?? Task.Delay;
	}

	/// <summary>Attempts per call, and the longest wait between attempts (Cloud Code functions have a time limit).</summary>
	public const int Attempts = 3;
	public static readonly TimeSpan MaxWait = TimeSpan.FromSeconds(2);

	public Task<ApiResult> StartAsync(StartRequest body, string idempotencyKey, CancellationToken ct = default) =>
		SendAsync(() =>
		{
			var req = new HttpRequestMessage(HttpMethod.Post, $"{_baseUrl}/v0/game-servers")
			{
				Content = new StringContent(JsonSerializer.Serialize(body, Json), Encoding.UTF8, "application/json"),
			};
			req.Headers.Add("Idempotency-Key", idempotencyKey);
			return req;
		}, retryInUse: true, ct);

	public Task<ApiResult> GetAsync(string id, CancellationToken ct = default) =>
		SendAsync(() => new HttpRequestMessage(HttpMethod.Get, $"{_baseUrl}/v0/game-servers/{Uri.EscapeDataString(id)}"), retryInUse: false, ct);

	private async Task<ApiResult> SendAsync(Func<HttpRequestMessage> make, bool retryInUse, CancellationToken ct)
	{
		ApiResult result = new() { Status = 0 };
		for (var attempt = 1; attempt <= Attempts; attempt++)
		{
			TimeSpan? retryAfter = null;
			try
			{
				using var req = make();
				req.Headers.Authorization = new AuthenticationHeaderValue("Bearer", _apiKey);
				req.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
				using var res = await _http.SendAsync(req, ct);
				var text = await res.Content.ReadAsStringAsync(ct);
				retryAfter = res.Headers.RetryAfter?.Delta;
				result = res.IsSuccessStatusCode
					? new ApiResult { Status = res.StatusCode, GameServer = Parse<GameServer>(text) }
					: new ApiResult { Status = res.StatusCode, Problem = Parse<Problem>(text) ?? new Problem { Detail = text } };
				if (result.Ok && string.IsNullOrEmpty(result.GameServer!.Id))
					result = new ApiResult { Status = res.StatusCode, Problem = new Problem { Code = "bad_reply", Detail = "no game server ID in the reply" } };
			}
			catch (HttpRequestException e)
			{
				result = new ApiResult { Status = 0, Problem = new Problem { Code = "network", Detail = e.Message } };
			}
			catch (TaskCanceledException) when (!ct.IsCancellationRequested)
			{
				result = new ApiResult { Status = 0, Problem = new Problem { Code = "timeout", Detail = "the API did not answer in time" } };
			}

			// The same start still running elsewhere (Unity retried the allocate call): wait for it to finish.
			var inUse = retryInUse && result.Problem?.Code == "idempotency_key_in_use";
			if (result.Ok || attempt == Attempts || !(result.Transient || inUse)) return result;
			var wait = retryAfter ?? TimeSpan.FromMilliseconds(250 * attempt);
			await _delay(wait > MaxWait ? MaxWait : wait, ct);
		}
		return result;
	}

	private static T? Parse<T>(string text) where T : class
	{
		try { return JsonSerializer.Deserialize<T>(text, Json); }
		catch (JsonException) { return null; }
	}
}
