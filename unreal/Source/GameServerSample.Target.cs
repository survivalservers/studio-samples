using UnrealBuildTool;

public class GameServerSampleTarget : TargetRules
{
	public GameServerSampleTarget(TargetInfo Target) : base(Target)
	{
		Type = TargetType.Game;
		DefaultBuildSettings = BuildSettingsVersion.Latest;
		IncludeOrderVersion = EngineIncludeOrderVersion.Latest;
		ExtraModuleNames.Add("GameServerSample");
	}
}
