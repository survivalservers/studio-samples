using UnrealBuildTool;

public class GameServerSample : ModuleRules
{
	public GameServerSample(ReadOnlyTargetRules Target) : base(Target)
	{
		PCHUsage = PCHUsageMode.UseExplicitOrSharedPCHs;
		PublicDependencyModuleNames.AddRange(new[] { "Core", "CoreUObject", "Engine" });
		// The Survival Servers Studio plugin (Plugins/SurvivalServersStudio): its subsystem makes the platform's calls.
		PrivateDependencyModuleNames.Add("SurvivalServersStudio");
	}
}
