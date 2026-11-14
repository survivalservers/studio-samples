// The smallest game server with the C# layer; see ../../c/sample/sample.c for what it shows.
using System;
using GameServerSdk;

try
{
    using var gs = Client.FromEnvironment();
    if (Environment.GetEnvironmentVariable("GS_SAMPLE_BACKGROUND") != null) return Background(gs);
    gs.Ready();
    var match = gs.WaitAllocation(TimeSpan.FromSeconds(30));
    if (match == null) return 3;
    Console.WriteLine($"match {match.ExternalId} map={(match.Env.TryGetValue("MAP", out var m) ? m : "-")}");
    gs.PlayerConnect("p1");
    gs.PlayerConnect("p2");
    try { gs.PlayerConnect("bad\0name"); return 1; } // a name a player chose, with a NUL in it: refused
    catch (GsException) { Console.WriteLine("refused a player ID with a control character"); }
    gs.PlayerDisconnect("p2");
    gs.SetLabel("mode", "ffa");
    gs.Health();
    gs.Tick(2.5, 6.25, 64);
    gs.Stop("match_over");
    var sd = gs.WaitShutdown(TimeSpan.FromSeconds(30));
    Console.WriteLine($"shutdown reason={sd?.Reason ?? "none"} deadline={(sd != null && sd.Deadline.ToUnixTimeMilliseconds() > 0 ? "set" : "none")}");
    return 0;
}
catch (GsException e)
{
    Console.Error.WriteLine($"gs: {e.Code} {e.Message}");
    return 1;
}

// With GS_SAMPLE_BACKGROUND set, the library's background helper sends health and calls the two lambdas once each.
static int Background(Client gs)
{
    using var matched = new System.Threading.ManualResetEventSlim(false);
    using var stopped = new System.Threading.ManualResetEventSlim(false);
    Shutdown? sd = null;
    gs.StartBackground(TimeSpan.FromMilliseconds(200), a =>
    {
        Console.WriteLine($"match {a.ExternalId} map={(a.Env.TryGetValue("MAP", out var m) ? m : "-")}");
        matched.Set();
    }, s => { sd = s; stopped.Set(); });
    gs.Ready();
    matched.Wait(TimeSpan.FromSeconds(30));
    gs.PlayerConnect("p1");
    gs.SetLabel("mode", "ffa");
    System.Threading.Thread.Sleep(1500); // the helper sends health meanwhile
    gs.Stop("match_over");
    stopped.Wait(TimeSpan.FromSeconds(30));
    Console.WriteLine($"shutdown reason={sd?.Reason ?? "none"} deadline={(sd != null && sd.Deadline.ToUnixTimeMilliseconds() > 0 ? "set" : "none")}");
    gs.StopBackground();
    return 0;
}
