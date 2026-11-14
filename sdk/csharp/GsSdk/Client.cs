// gs_sdk for C#: a thin layer over the C library, with exceptions and .NET types. Apache-2.0.
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Threading;

namespace GameServerSdk
{
    /// <summary>A failed call: its code and the agent's or the library's reason.</summary>
    public sealed class GsException : Exception
    {
        public int Code { get; }
        public GsException(int code, string message) : base(message) { Code = code; }
    }

    /// <summary>The game server's match: its IDs, settings, labels and command-line arguments.</summary>
    public sealed class Allocation
    {
        public string GameServerId { get; internal set; } = "";
        public string ExternalId { get; internal set; } = "";
        public List<string> Args { get; } = new List<string>();
        public Dictionary<string, string> Env { get; } = new Dictionary<string, string>();
        public Dictionary<string, string> Labels { get; } = new Dictionary<string, string>();
    }

    /// <summary>A shutdown notice: save and exit before the deadline.</summary>
    public sealed class Shutdown
    {
        public DateTimeOffset Deadline { get; internal set; }
        public string Reason { get; internal set; } = "";
    }

    /// <summary>The game server's side of the local API.</summary>
    public sealed class Client : IDisposable
    {
        const int Ok = 0, NoAgent = -1, NoneYet = -4;
        IntPtr c;

        Client(IntPtr c) { this.c = c; }

        /// <summary>Connects as the game server the machine started (GS_LOCAL_API_URL, GS_TOKEN).</summary>
        const int Unsupported = -8; // GS_ERR_UNSUPPORTED

        public static Client FromEnvironment()
        {
            int r = Native.gs_open(out IntPtr c);
            if (r != Ok) throw new GsException(r, r == NoAgent ? "no local API here (GS_LOCAL_API_URL and GS_TOKEN are not set)" : "gs_open failed");
            return new Client(c);
        }

        public static Client Open(string agentUrl, string token)
        {
            int r = Native.gs_open_with(agentUrl, token, out IntPtr c);
            if (r != Ok) throw new GsException(r, "gs_open_with failed");
            return new Client(c);
        }

        public void Ready() => Check(Native.gs_ready(c));
        public void Health() => Check(Native.gs_health(c));
        public void Stop(string reason) => Check(Native.gs_stop(c, reason));

        /// <summary>A persistent server just finished a save: it is backed up at once, and with keep, kept under that name.
        /// An agent older than this call is not an error: the folder watcher backs saves up without it.</summary>
        public void Saved(string keep = null)
        {
            int r = Native.gs_saved(c, keep);
            if (r != Unsupported) Check(r);
        }
        /// <summary>An ID is 1 to 128 characters with no control characters; any other (often a name a player chose) is
        /// refused (code -5) and never joins the list.</summary>
        public void PlayerConnect(string id)
        {
            if (id != null && id.IndexOf('\0') >= 0) // a C string would end at the NUL and report another player
                throw new GsException(-5, "the player ID has a control character (1 to 128 characters, no control characters)");
            Check(Native.gs_player_connect(c, id));
        }
        public void PlayerDisconnect(string id) => Check(Native.gs_player_disconnect(c, id));
        public void SetPlayerCount(int count) => Check(Native.gs_set_player_count(c, count));
        public void SetLabel(string name, string value) => Check(Native.gs_set_label(c, name, value));
        public void RemoveLabel(string name) => Check(Native.gs_set_label(c, name, null));
        public void Tick(double p50Ms, double p99Ms, int hz = 0) => Check(Native.gs_tick(c, p50Ms, p99Ms, hz));

        /// <summary>Waits up to <paramref name="wait"/> for the match; null when there is none yet.</summary>
        public Allocation? WaitAllocation(TimeSpan wait)
        {
            int r = Native.gs_wait_allocation(c, (int)wait.TotalMilliseconds, out IntPtr a);
            if (r == NoneYet) return null;
            Check(r);
            try
            {
                var out_ = new Allocation
                {
                    GameServerId = Str(Native.gs_allocation_game_server_id(a)),
                    ExternalId = Str(Native.gs_allocation_external_id(a)),
                };
                for (UIntPtr i = UIntPtr.Zero; ; i = (UIntPtr)((ulong)i + 1))
                {
                    IntPtr s = Native.gs_allocation_arg(a, i);
                    if (s == IntPtr.Zero) break;
                    out_.Args.Add(Str(s));
                }
                for (UIntPtr i = UIntPtr.Zero; ; i = (UIntPtr)((ulong)i + 1))
                {
                    IntPtr n = Native.gs_allocation_env_name(a, i);
                    if (n == IntPtr.Zero) break;
                    out_.Env[Str(n)] = Str(Native.gs_allocation_env_value(a, i));
                }
                for (UIntPtr i = UIntPtr.Zero; ; i = (UIntPtr)((ulong)i + 1))
                {
                    IntPtr n = Native.gs_allocation_label_name(a, i);
                    if (n == IntPtr.Zero) break;
                    out_.Labels[Str(n)] = Str(Native.gs_allocation_label_value(a, i));
                }
                return out_;
            }
            finally { Native.gs_allocation_free(a); }
        }

        /// <summary>Waits up to <paramref name="wait"/> for a shutdown notice; null when there is none.</summary>
        public Shutdown? WaitShutdown(TimeSpan wait)
        {
            var reason = new byte[128];
            int r = Native.gs_wait_shutdown(c, (int)wait.TotalMilliseconds, out long deadline, reason, (UIntPtr)reason.Length);
            if (r == NoneYet) return null;
            Check(r);
            int len = Array.IndexOf(reason, (byte)0);
            return new Shutdown
            {
                Deadline = DateTimeOffset.FromUnixTimeMilliseconds(deadline),
                Reason = System.Text.Encoding.UTF8.GetString(reason, 0, len < 0 ? reason.Length : len),
            };
        }

        sealed class Background
        {
            public readonly ManualResetEventSlim Stop = new ManualResetEventSlim(false);
            public volatile bool Ended; // the stop notice came: the match wait answers at once from then on
            public readonly List<Thread> Threads = new List<Thread>();
        }

        readonly object bgLock = new object();
        Background? bg;

        /// <summary>Sends health every <paramref name="healthEvery"/> (zero: none) and calls <paramref name="onMatch"/>
        /// and <paramref name="onStop"/> once each, from threads of its own, when the match arrives and when the stop
        /// notice comes (null: not waited for), as the C library's gs_background_start does. In Unity, hand what they do
        /// to the main thread. <see cref="StopBackground"/> or Dispose ends it within about 5 seconds; never call either
        /// from inside a callback. An exception a callback throws ends that callback only.</summary>
        public void StartBackground(TimeSpan healthEvery, Action<Allocation>? onMatch, Action<Shutdown>? onStop)
        {
            if (healthEvery < TimeSpan.Zero || (healthEvery == TimeSpan.Zero && onMatch == null && onStop == null))
                throw new GsException(-5, "the background helper needs health, onMatch or onStop");
            var b = new Background();
            lock (bgLock)
            {
                if (bg != null) throw new GsException(-5, "the background helper is already running");
                bg = b;
            }
            if (healthEvery > TimeSpan.Zero)
                Run(b, "gs health", () =>
                {
                    do { try { Health(); } catch (GsException) { } } while (!b.Stop.Wait(healthEvery));
                });
            if (onMatch != null)
                Run(b, "gs match", () =>
                {
                    while (!b.Stop.IsSet)
                    {
                        Allocation? a = null;
                        bool failed = false;
                        try { a = WaitAllocation(TimeSpan.FromSeconds(5)); } catch (GsException) { failed = true; }
                        if (a != null) { Quietly(() => onMatch(a)); return; }
                        if (b.Ended) return;
                        if (failed && b.Stop.Wait(TimeSpan.FromSeconds(1))) return; // the agent is away for a moment: ask again
                    }
                });
            if (onStop != null)
                Run(b, "gs shutdown", () =>
                {
                    while (!b.Stop.IsSet)
                    {
                        Shutdown? s = null;
                        bool failed = false;
                        try { s = WaitShutdown(TimeSpan.FromSeconds(5)); } catch (GsException) { failed = true; }
                        if (s != null) { b.Ended = true; Quietly(() => onStop(s)); return; }
                        if (failed && b.Stop.Wait(TimeSpan.FromSeconds(1))) return;
                    }
                });
        }

        /// <summary>Ends <see cref="StartBackground"/>'s helper and waits for it (up to about 5 seconds); never from a
        /// callback.</summary>
        public void StopBackground()
        {
            Background? b;
            lock (bgLock) { b = bg; bg = null; }
            if (b == null) return;
            b.Stop.Set();
            foreach (var t in b.Threads) t.Join();
        }

        static void Run(Background b, string name, Action body)
        {
            var t = new Thread(() => body()) { IsBackground = true, Name = name };
            b.Threads.Add(t);
            t.Start();
        }

        static void Quietly(Action a)
        {
            try { a(); } catch (Exception) { }
        }

        public void Dispose()
        {
            StopBackground();
            if (c != IntPtr.Zero)
            {
                Native.gs_close(c);
                c = IntPtr.Zero;
            }
        }

        void Check(int r)
        {
            if (r != Ok) throw new GsException(r, Str(Native.gs_last_error(c)));
        }

        static string Str(IntPtr p) => p == IntPtr.Zero ? "" : Marshal.PtrToStringUTF8(p) ?? "";

        static class Native
        {
            const string Lib = "gs_sdk"; // gs_sdk.dll, libgs_sdk.so
            [DllImport(Lib)] public static extern int gs_open(out IntPtr c);
            [DllImport(Lib)] public static extern int gs_open_with([MarshalAs(UnmanagedType.LPUTF8Str)] string url, [MarshalAs(UnmanagedType.LPUTF8Str)] string token, out IntPtr c);
            [DllImport(Lib)] public static extern void gs_close(IntPtr c);
            [DllImport(Lib)] public static extern IntPtr gs_last_error(IntPtr c);
            [DllImport(Lib)] public static extern int gs_ready(IntPtr c);
            [DllImport(Lib)] public static extern int gs_health(IntPtr c);
            [DllImport(Lib)] public static extern int gs_stop(IntPtr c, [MarshalAs(UnmanagedType.LPUTF8Str)] string reason);
            [DllImport(Lib)] public static extern int gs_saved(IntPtr c, [MarshalAs(UnmanagedType.LPUTF8Str)] string keep);
            [DllImport(Lib)] public static extern int gs_wait_allocation(IntPtr c, int waitMs, out IntPtr a);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_game_server_id(IntPtr a);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_external_id(IntPtr a);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_arg(IntPtr a, UIntPtr i);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_env_name(IntPtr a, UIntPtr i);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_env_value(IntPtr a, UIntPtr i);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_label_name(IntPtr a, UIntPtr i);
            [DllImport(Lib)] public static extern IntPtr gs_allocation_label_value(IntPtr a, UIntPtr i);
            [DllImport(Lib)] public static extern void gs_allocation_free(IntPtr a);
            [DllImport(Lib)] public static extern int gs_wait_shutdown(IntPtr c, int waitMs, out long deadlineUnixMs, byte[] reason, UIntPtr reasonSize);
            [DllImport(Lib)] public static extern int gs_player_connect(IntPtr c, [MarshalAs(UnmanagedType.LPUTF8Str)] string id);
            [DllImport(Lib)] public static extern int gs_player_disconnect(IntPtr c, [MarshalAs(UnmanagedType.LPUTF8Str)] string id);
            [DllImport(Lib)] public static extern int gs_set_player_count(IntPtr c, int count);
            [DllImport(Lib)] public static extern int gs_set_label(IntPtr c, [MarshalAs(UnmanagedType.LPUTF8Str)] string name, [MarshalAs(UnmanagedType.LPUTF8Str)] string? value);
            [DllImport(Lib)] public static extern int gs_tick(IntPtr c, double p50Ms, double p99Ms, int hz);
        }
    }
}
