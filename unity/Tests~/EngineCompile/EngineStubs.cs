// Stand-ins for the UnityEngine and UnityEditor members the sample uses, with the shapes Unity documents. For the
// compile check only; never shipped.
using System;
using System.Threading.Tasks;

namespace UnityEngine
{
	public class Object { }
	public class Component : Object { }
	public class Behaviour : Component { public bool enabled { get; set; } }
	public class MonoBehaviour : Behaviour { }

	[AttributeUsage(AttributeTargets.Field)]
	public sealed class TooltipAttribute : Attribute { public TooltipAttribute(string tooltip) { } }

	public static class Application
	{
		public static bool isBatchMode => true;
		public static bool isEditor => false;
		public static int targetFrameRate { get; set; }
		public static event Action quitting;
		public static void Quit(int exitCode) => quitting?.Invoke();
	}

	public static class Debug
	{
		public static void Log(object message) { }
		public static void LogError(object message) { }
	}
}

namespace UnityEngine.Networking
{
	public class DownloadHandler { public string text => ""; }
	public sealed class DownloadHandlerBuffer : DownloadHandler { }
	public class UploadHandler { }
	public sealed class UploadHandlerRaw : UploadHandler { public UploadHandlerRaw(byte[] data) { } }
	public sealed class UnityWebRequestAsyncOperation { public bool isDone => true; }

	public sealed class UnityWebRequest : IDisposable
	{
		public UnityWebRequest(string url, string method) { }
		public DownloadHandler downloadHandler { get; set; }
		public UploadHandler uploadHandler { get; set; }
		public int timeout { get; set; }
		public long responseCode => 0;
		public void SetRequestHeader(string name, string value) { }
		public UnityWebRequestAsyncOperation SendWebRequest() => new UnityWebRequestAsyncOperation();
		public void Abort() { }
		public void Dispose() { }
	}
}

namespace UnityEditor
{
	[AttributeUsage(AttributeTargets.Method)]
	public sealed class MenuItem : Attribute { public MenuItem(string itemName) { } }
	public enum BuildTarget { StandaloneWindows64, StandaloneLinux64 }
	public enum StandaloneBuildSubtarget { Player, Server }
	[Flags] public enum BuildOptions { None = 0 }
	public struct BuildPlayerOptions
	{
		public string[] scenes;
		public BuildTarget target;
		public int subtarget;
		public string locationPathName;
		public BuildOptions options;
	}
	public sealed class EditorBuildSettingsScene { public bool enabled; public string path; }
	public static class EditorBuildSettings { public static EditorBuildSettingsScene[] scenes => new EditorBuildSettingsScene[0]; }
	public static class BuildPipeline { public static Build.Reporting.BuildReport BuildPlayer(BuildPlayerOptions options) => new Build.Reporting.BuildReport(); }
	public static class EditorApplication { public static void Exit(int code) { } }
}

namespace UnityEditor.Build.Reporting
{
	public enum BuildResult { Unknown, Succeeded, Failed, Cancelled }
	public struct BuildSummary { public BuildResult result; }
	public sealed class BuildReport { public BuildSummary summary; }
}
