using System.IO;
using System.IO.Compression;
using System.Linq;
using UnityEditor;
using UnityEngine;

namespace GameServerSample.Editor
{
	/// <summary>
	/// Dedicated server builds for the platform: a Windows zip uploaded as it is, and a Linux build the Dockerfile
	/// packs into an image. From the menu (Game Server) or the command line:
	///   Unity -batchmode -quit -projectPath . -executeMethod GameServerSample.Editor.GameServerBuild.Windows
	///   Unity -batchmode -quit -projectPath . -executeMethod GameServerSample.Editor.GameServerBuild.Linux
	/// Needs the Dedicated Server build support module for each platform.
	/// </summary>
	public static class GameServerBuild
	{
		[MenuItem("Survival Servers Studio/Build Windows server (zip)")]
		public static void Windows()
		{
			const string folder = "Builds/WindowsServer";
			Build(BuildTarget.StandaloneWindows64, $"{folder}/GameServer.exe");
			var zip = "Builds/GameServer-windows.zip";
			if (File.Exists(zip)) File.Delete(zip);
			ZipFile.CreateFromDirectory(folder, zip);
			Debug.Log($"Upload {zip}; its executable is GameServer.exe");
		}

		[MenuItem("Survival Servers Studio/Build Linux server (for the image)")]
		public static void Linux() => Build(BuildTarget.StandaloneLinux64, "Builds/LinuxServer/GameServer.x86_64");

		private static void Build(BuildTarget target, string path)
		{
			var options = new BuildPlayerOptions
			{
				scenes = EditorBuildSettings.scenes.Where(s => s.enabled).Select(s => s.path).ToArray(),
				target = target,
				subtarget = (int)StandaloneBuildSubtarget.Server,
				locationPathName = path,
				options = BuildOptions.None,
			};
			var report = BuildPipeline.BuildPlayer(options);
			if (report.summary.result != UnityEditor.Build.Reporting.BuildResult.Succeeded)
			{
				Debug.LogError($"Server build failed: {report.summary.result}");
				if (Application.isBatchMode) EditorApplication.Exit(1);
			}
		}
	}
}
