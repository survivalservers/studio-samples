using UnrealBuildTool;

public class GameServerSampleServerTarget : TargetRules
{
	public GameServerSampleServerTarget(TargetInfo Target) : base(Target)
	{
		Type = TargetType.Server;
		DefaultBuildSettings = BuildSettingsVersion.Latest;
		IncludeOrderVersion = EngineIncludeOrderVersion.Latest;
		ExtraModuleNames.Add("GameServerSample");
		// A Shipping server writes no log at all by default; the platform keeps the console as the game server's log.
		// Changing it needs a build environment of its own (needs a source build of the engine, as servers do anyway).
		bUseLoggingInShipping = true;
		BuildEnvironment = TargetBuildEnvironment.Unique;
	}
}
