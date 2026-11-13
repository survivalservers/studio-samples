using Flax.Build;
using Flax.Build.NativeCpp;

public class Game : GameModule
{
    /// <inheritdoc />
    public override void Init()
    {
        base.Init();

        // C#-only scripting
        BuildNativeCode = false;
    }

    /// <inheritdoc />
    public override void Setup(BuildOptions options)
    {
        base.Setup(options);

        options.ScriptingAPI.IgnoreMissingDocumentationWarnings = true;

        // The .NET libraries the server uses: the game's UDP socket, the local API over HTTP and its JSON.
        options.ScriptingAPI.SystemReferences.Add("System.Net.Primitives");
        options.ScriptingAPI.SystemReferences.Add("System.Net.Sockets");
        options.ScriptingAPI.SystemReferences.Add("System.Net.Http");
        options.ScriptingAPI.SystemReferences.Add("System.Text.Json");
        options.ScriptingAPI.SystemReferences.Add("System.Linq");
        options.ScriptingAPI.SystemReferences.Add("System.Collections");
        options.ScriptingAPI.SystemReferences.Add("Microsoft.Win32.Primitives"); // SocketException's base
    }
}
