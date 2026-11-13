// A small game server for O3DE's server launchers, with no plugin of ours: plain sockets for the game's UDP and for
// the local API (HTTP/1.0, one call at a time on its own thread, so a long-poll never holds the game's tick).
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
#pragma once

#include <atomic>
#include <chrono>
#include <mutex>
#include <set>
#include <string>
#include <thread>
#include <vector>

namespace LabServer
{
    class LabGame
    {
    public:
        ~LabGame();
        // Binds the game port and starts the calls to the local API. False when the port can't be bound.
        bool Start();
        // The players' packets: call every tick.
        void Poll();
        // True once the game has decided to exit (the match over and reported, or a stop asked for).
        bool Done() const { return m_done; }

    private:
        struct Reply { int status = 0; std::string body; };
        Reply Call(const std::string& method, const std::string& path, const std::string& body = "", int timeoutSec = 40);
        std::string Handle(const std::string& line);
        void ReportPlayers();
        void Finish(const std::string& reason, bool selfStop);
        void WriteResult(const std::string& reason, long long seconds);
        void Run(); // the local API's own thread: allocation, ready, the match, the shutdown notice

        std::string m_agentHost;
        int m_agentPort = 0;
        std::string m_token;
        std::string m_gsId = "local";
        std::string m_match;
        bool m_hasAllocation = false;
        long long m_sock = -1; // the UDP socket (an int or a SOCKET)
        std::mutex m_mu;       // players, peak, the pending player report
        std::set<std::string> m_players;
        int m_peak = 0;
        bool m_joined = false;
        bool m_reportPending = false;
        std::atomic<bool> m_ending{ false }; // the last player left: report, then end the match
        std::atomic<bool> m_quit{ false };   // the launcher is shutting down: the API thread just leaves
        std::atomic<bool> m_done{ false };
        std::thread m_api;
        std::chrono::steady_clock::time_point m_started = std::chrono::steady_clock::now();
    };
}
