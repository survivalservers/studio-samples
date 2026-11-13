// A sample game server for Flax Engine, run headless (-headless -null -mute -std), with no plugin of ours. A game
// plugin starts with the game, so the server needs no scene. Everything the platform offers is plain HTTP, so .NET's
// own HttpClient does it all, and a UdpClient carries the game:
//
// - the port and the match settings come from GS_* environment variables and the allocation call;
// - it says it is ready on the local API (or a version can watch for the line it prints);
// - pre-started as a ready server, it waits for its match on a long-poll;
// - it reports its players, ends the match by stopping itself, and saves on the way out when asked to stop (the stop
//   notice on the local API);
// - it leaves a result file to be collected after it exits.
//
// The "game" is a tiny UDP protocol: "join <name>", "leave <name>", "ping". The match ends when everyone who joined
// has left. With no GS_* variables it runs as a plain local server on port 7777.
using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using FlaxEngine;

namespace Game;

public class LabServer : GamePlugin
{
    const string ReadyLine = "Server ready for players";
    const string LongPoll = "30s";

    readonly string _agentUrl = (Environment.GetEnvironmentVariable("GS_LOCAL_API_URL") is { Length: > 0 } u ? u : Environment.GetEnvironmentVariable("GS_AGENT_URL") ?? "").TrimEnd('/');
    readonly string _gsId = Environment.GetEnvironmentVariable("GS_ID") ?? "local";
    readonly HttpClient _http = new() { Timeout = TimeSpan.FromSeconds(40) }; // longer than a long-poll's wait
    readonly object _lock = new();
    readonly SortedSet<string> _players = new(StringComparer.Ordinal);
    readonly SemaphoreSlim _reporting = new(1, 1);
    readonly DateTime _started = DateTime.UtcNow;
    UdpClient _udp;
    JsonElement? _allocation;
    int _peak;
    bool _joined;
    volatile bool _ending;
    volatile int _exitCode = -1; // set from a task, acted on in the game's own update

    public override void Initialize()
    {
        base.Initialize();
#if FLAX_EDITOR
        return; // the editor loads game plugins too: the server starts only in the built game
#else
        var token = Environment.GetEnvironmentVariable("GS_TOKEN") ?? "";
        _http.DefaultRequestHeaders.Add("Authorization", "Bearer " + token);
        var port = ListenPort();
        try
        {
            _udp = new UdpClient(new IPEndPoint(IPAddress.Any, port));
        }
        catch (SocketException e)
        {
            Console.WriteLine($"bind game port {port}: {e.Message}");
            Engine.RequestExit(1);
            return;
        }
        Console.WriteLine($"listening on udp {port}; players connect to {Environment.GetEnvironmentVariable("GS_PUBLIC_IP")}:{Environment.GetEnvironmentVariable("GS_PORT_GAME")}");
        Scripting.Update += OnUpdate;
        if (_agentUrl == "")
        {
            Console.WriteLine(ReadyLine);
            return;
        }
        _ = Task.Run(StartAsync);
#endif
    }

    public override void Deinitialize()
    {
        Scripting.Update -= OnUpdate;
        _udp?.Dispose();
        base.Deinitialize();
    }

    // Linux images listen on the container port; Windows builds bind the public port itself.
    static int ListenPort()
    {
        foreach (var name in new[] { "GS_PORT_GAME_INTERNAL", "GS_PORT_GAME" })
            if (int.TryParse(Environment.GetEnvironmentVariable(name), out var n) && n > 0)
                return n;
        return 7777;
    }

    async Task StartAsync()
    {
        // A fresh start is allocated before it starts: read the match now, before saying ready, so it is set up before
        // the first player arrives. A ready server (pre-started) has none yet (204) and waits for it after ready.
        var (status, body) = await Call(HttpMethod.Get, "/v0/self/allocation?wait=0s");
        if (status == 200)
            SetAllocation(body);
        // A version can use this exact line as its ready check instead of the call below.
        Console.WriteLine(ReadyLine);
        (status, body) = await Call(HttpMethod.Post, "/v0/self/ready");
        if (status < 200 || status >= 300)
        {
            Console.WriteLine($"say ready: {status} {body}");
            _exitCode = 1;
            return;
        }
        _ = Task.Run(WaitForAllocation);
        _ = Task.Run(WaitForStop);
    }

    async Task WaitForAllocation()
    {
        while (_allocation == null && !_ending)
        {
            var (status, body) = await Call(HttpMethod.Get, "/v0/self/allocation?wait=" + LongPoll);
            if (status == 200)
                SetAllocation(body);
            else if (status != 204)
                await Task.Delay(1000);
        }
    }

    async Task WaitForStop()
    {
        while (!_ending)
        {
            var (status, _) = await Call(HttpMethod.Get, "/v0/self/shutdown?wait=" + LongPoll);
            if (status == 200)
            {
                Console.WriteLine("stop asked; saving and leaving");
                await Finish("stop_asked", false);
                return;
            }
            if (status != 204 && !_ending)
                await Task.Delay(1000);
        }
    }

    void SetAllocation(string body)
    {
        using var doc = JsonDocument.Parse(body);
        var data = doc.RootElement.Clone();
        _allocation = data;
        // Setting names only: values can be secret, and the console is a log kept for 90 days.
        var match = data.TryGetProperty("external_id", out var m) ? m.GetString() : "";
        var keys = data.TryGetProperty("env", out var env) && env.ValueKind == JsonValueKind.Object
            ? string.Join(",", env.EnumerateObject().Select(p => p.Name).OrderBy(n => n, StringComparer.Ordinal))
            : "";
        Console.WriteLine($"allocated: match \"{match}\", settings {keys}");
    }

    // The game's own update, on the main thread: the players' packets, and the exit once a task decided on one.
    void OnUpdate()
    {
        if (_exitCode >= 0)
        {
            Engine.RequestExit(_exitCode);
            _exitCode = -2;
            return;
        }
        while (!_ending && _udp.Available > 0)
        {
            var from = new IPEndPoint(IPAddress.Any, 0);
            var line = Encoding.UTF8.GetString(_udp.Receive(ref from)).Trim();
            var reply = Handle(line);
            if (reply != "")
                _udp.Send(Encoding.UTF8.GetBytes(reply), from);
        }
    }

    // Runs one command from a player.
    string Handle(string line)
    {
        var parts = line.Split(' ', 2);
        var cmd = parts[0];
        var who = parts.Length > 1 ? parts[1].Trim() : "";
        if (cmd == "ping")
            return "pong";
        lock (_lock)
        {
            if (cmd == "join" && who != "")
            {
                // The platform refuses a player list holding such a name, so it never joins; the others keep counting.
                if (who.Length > 128 || who.Any(c => c < 0x20 || (c >= 0x7f && c <= 0x9f)))
                    return "refused: a name is 1 to 128 characters with no control characters";
                if (!_players.Add(who))
                    return "already in " + _gsId;
                _joined = true;
                _peak = Math.Max(_peak, _players.Count);
                Console.WriteLine($"{who} joined ({_players.Count} playing)");
                _ = ReportPlayers();
                return $"welcome {who} to {_gsId}";
            }
            if (cmd == "leave" && who != "")
            {
                if (!_players.Remove(who))
                    return "not in " + _gsId;
                Console.WriteLine($"{who} left ({_players.Count} playing)");
                _ = ReportPlayers();
                if (_joined && _players.Count == 0)
                    _ = Finish("match_over", true);
                return "bye " + who;
            }
        }
        return "unknown command; try join <name>, leave <name> or ping";
    }

    // Sends the player list, one call at a time, always the newest list.
    async Task ReportPlayers()
    {
        if (_agentUrl == "")
            return;
        await _reporting.WaitAsync();
        try
        {
            string[] ids;
            lock (_lock)
                ids = _players.ToArray();
            await Call(HttpMethod.Put, "/v0/self/players", JsonSerializer.Serialize(new { ids }));
        }
        finally
        {
            _reporting.Release();
        }
    }

    // Ends the match: the last player list goes out, the result is saved, and if the game server decided to end
    // (rather than being asked to stop) it tells the platform, which is the same as exiting with code 0.
    async Task Finish(string reason, bool selfStop)
    {
        if (_ending)
            return;
        _ending = true;
        await _reporting.WaitAsync();
        _reporting.Release();
        var seconds = (int)(DateTime.UtcNow - _started).TotalSeconds;
        Console.WriteLine($"match over: {reason} after {seconds}s, {_peak} players at most");
        WriteResult(reason, seconds);
        if (selfStop && _agentUrl != "")
            await Call(HttpMethod.Post, "/v0/self/stop", JsonSerializer.Serialize(new { reason }));
        _exitCode = 0;
    }

    // match-result.json in GS_ARTIFACT_DIR is collected after the game server exits.
    void WriteResult(string reason, int seconds)
    {
        var dir = Environment.GetEnvironmentVariable("GS_ARTIFACT_DIR");
        if (string.IsNullOrEmpty(dir))
            return;
        var result = new Dictionary<string, object>
        {
            ["game_server_id"] = _gsId, ["reason"] = reason, ["peak_players"] = _peak, ["seconds"] = seconds,
        };
        if (_allocation is { } a && a.TryGetProperty("external_id", out var m))
            result["match"] = m.GetString();
        File.WriteAllText(Path.Combine(dir, "match-result.json"), JsonSerializer.Serialize(result) + "\n");
    }

    // One call to the local API. Status 0 when the call did not complete.
    async Task<(int, string)> Call(HttpMethod method, string path, string json = null)
    {
        try
        {
            using var req = new HttpRequestMessage(method, _agentUrl + path);
            if (json != null)
                req.Content = new StringContent(json, Encoding.UTF8, "application/json");
            using var res = await _http.SendAsync(req);
            return ((int)res.StatusCode, await res.Content.ReadAsStringAsync());
        }
        catch (Exception)
        {
            return (0, "");
        }
    }
}
