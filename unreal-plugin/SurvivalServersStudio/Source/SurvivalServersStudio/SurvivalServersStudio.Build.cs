using UnrealBuildTool;

public class SurvivalServersStudio : ModuleRules
{
	public SurvivalServersStudio(ReadOnlyTargetRules Target) : base(Target)
	{
		// Each file compiles on its own, with no precompiled header and no unity build, in every build of every
		// project, so a missing include fails at once. Four small files: it costs a studio's build nothing.
		PCHUsage = PCHUsageMode.NoPCHs;
		bUseUnity = false;
		PublicDependencyModuleNames.AddRange(new[] { "Core", "CoreUObject", "Engine", "DeveloperSettings" });
		// The local API is plain HTTP and JSON on the machine: Unreal's own modules, nothing else. CoreOnline has the
		// players' IDs.
		PrivateDependencyModuleNames.AddRange(new[] { "HTTP", "Json", "CoreOnline" });
	}
}
