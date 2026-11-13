using UnrealBuildTool;

public class GameServerSampleEditorTarget : TargetRules
{
	public GameServerSampleEditorTarget(TargetInfo Target) : base(Target)
	{
		Type = TargetType.Editor;
		DefaultBuildSettings = BuildSettingsVersion.Latest;
		IncludeOrderVersion = EngineIncludeOrderVersion.Latest;
		ExtraModuleNames.Add("GameServerSample");
	}
}
