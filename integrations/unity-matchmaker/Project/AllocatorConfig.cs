using System;
using System.Collections.Generic;

namespace GameServerAllocatorModule;

/// <summary>Where a match's game server may run: preferred regions first, then any region in the continents.</summary>
public sealed class PlacementChoice
{
	/// <summary>Region IDs such as <c>europe-west</c>, in order of preference.</summary>
	public List<string> Regions { get; set; } = new();

	/// <summary>Continent IDs such as <c>europe</c>, used after <see cref="Regions"/>.</summary>
	public List<string> Continents { get; set; } = new();

	internal bool IsEmpty => Regions.Count == 0 && Continents.Count == 0;
}

/// <summary>
/// Settings for the allocator, set in <see cref="ModuleConfig"/>. Only <see cref="App"/> is required: everything else
/// falls back to the app's own defaults in the API.
/// </summary>
public sealed class AllocatorConfig
{
	/// <summary>The address used when <see cref="ApiBaseUrl"/> is not set: a placeholder until the service's public address
	/// is published.</summary>
	public const string DefaultApiBaseUrl = "https://studio.survivalservers.com";

	/// <summary>The API address; empty means <see cref="DefaultApiBaseUrl"/>. Under a test host (NUnit, xUnit, MSTest) an
	/// empty address is refused, so a test always names its own.</summary>
	public string? ApiBaseUrl { get; set; }

	/// <summary>The name of the Unity secret that holds your API key (scopes <c>game_servers:write</c> and <c>game_servers:read</c>).</summary>
	public string ApiKeySecretName { get; set; } = "GAME_SERVER_API_KEY";

	/// <summary>Required: the app's slug or ID.</summary>
	public string App { get; set; } = "";

	/// <summary>A version name to start. Leave empty to use <see cref="Branch"/>.</summary>
	public string? Version { get; set; }

	/// <summary>The branch to start when no <see cref="Version"/> is set. Empty means the app's <c>live</c> branch.</summary>
	public string? Branch { get; set; }

	/// <summary>The named port players connect to, as in the version's <c>launch.ports</c>.</summary>
	public string PortName { get; set; } = "game";

	/// <summary><c>auto</c> (a ready server if one is waiting, else a cold start), <c>warm</c> (ready servers only) or <c>cold</c>.</summary>
	public string StartMode { get; set; } = "auto";

	/// <summary>
	/// Placement by the match's <c>Region</c> property (set by Unity's region selection), for example
	/// <c>{"us-east": new PlacementChoice { Regions = { "us-east" } }}</c>. Checked first.
	/// </summary>
	public Dictionary<string, PlacementChoice> PlacementByRegion { get; set; } = new();

	/// <summary>Placement by pool name, for queues with one pool per region. Checked second.</summary>
	public Dictionary<string, PlacementChoice> PlacementByPool { get; set; } = new();

	/// <summary>Used when neither map matches. Empty: the app's default placement.</summary>
	public PlacementChoice DefaultPlacement { get; set; } = new();

	/// <summary>Stop the game server after this long even if it never stops itself. Null: no limit.</summary>
	public TimeSpan? TimeLimit { get; set; }

	/// <summary>
	/// Pass the match's properties (teams and players) to the game server as the <c>MATCH_PROPERTIES</c> setting, in
	/// JSON. It arrives in the allocation data and, on a cold start, in the environment.
	/// </summary>
	public bool SendMatchProperties { get; set; } = true;
}
