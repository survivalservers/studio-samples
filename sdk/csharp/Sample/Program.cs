// The smallest game server with the C# layer; see ../../c/sample/sample.c for what it shows.
using System;
using GameServerSdk;

try
{
    using var gs = Client.FromEnvironment();
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
