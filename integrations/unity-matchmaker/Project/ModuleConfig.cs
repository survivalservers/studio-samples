using System;
using System.Collections.Generic;
using System.Net.Http;
using Microsoft.Extensions.DependencyInjection;
using Unity.Services.CloudCode.Apis.Extensions;
using Unity.Services.CloudCode.Core;

namespace GameServerAllocatorModule;

/// <summary>
/// The module's setup. Edit the <see cref="AllocatorConfig"/> below for your game, then deploy the module.
/// The API key goes in a Unity secret (Administration, Secrets) named GAME_SERVER_API_KEY, never in this file.
/// </summary>
public class ModuleConfig : ICloudCodeSetup
{
	public void Setup(ICloudCodeConfig config)
	{
		config.AddSecretClient();
		config.Dependencies.AddSingleton<IHttpClientProvider, SharedHttpClient>();
		config.Dependencies.AddSingleton(new AllocatorConfig
		{
			// Required: your app's slug, as in the panel.
			App = "your-app",

			// Required: the API's address (there is no default yet).
			ApiBaseUrl = "https://studio.survivalservers.com",

			// Optional: start this version, or a branch other than "live".
			// Version = "1.4.2",
			// Branch = "beta",

			// Where to run each match, by the Region Unity picked for it (checked first) ...
			// PlacementByRegion = new Dictionary<string, PlacementChoice>
			// {
			//     ["us-east"] = new PlacementChoice { Regions = { "us-east" }, Continents = { "north-america" } },
			//     ["eu-west"] = new PlacementChoice { Continents = { "europe" } },
			// },
			// ... or by pool name, for queues with one pool per region.
			// PlacementByPool = new Dictionary<string, PlacementChoice> { ["eu-pool"] = new PlacementChoice { Continents = { "europe" } } },
			// Otherwise: DefaultPlacement, such as new PlacementChoice { Continents = { "north-america" } },
			// or the app's own default placement when that is empty.

			// Optional: stop a match that never ends by itself.
			// TimeLimit = TimeSpan.FromHours(2),
		});
	}
}

/// <summary>Gives the allocator its HTTP client; one shared client, as .NET recommends.</summary>
public interface IHttpClientProvider
{
	HttpClient Get();
}

public sealed class SharedHttpClient : IHttpClientProvider
{
	private static readonly HttpClient Client = new() { Timeout = TimeSpan.FromSeconds(5) };

	public HttpClient Get() => Client;
}
