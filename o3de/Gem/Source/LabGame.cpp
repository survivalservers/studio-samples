#include "LabGame.h"

#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#pragma comment(lib, "Ws2_32.lib")
using Sock = SOCKET;
static const Sock NoSock = INVALID_SOCKET;
static void CloseSock(Sock s) { closesocket(s); }
#else
#include <arpa/inet.h>
#include <fcntl.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>
using Sock = int;
static const Sock NoSock = -1;
static void CloseSock(Sock s) { close(s); }
#endif

namespace LabServer
{
    static const char* ReadyLine = "Server ready for players";

    static std::string Env(const char* name, const char* fallback = "")
    {
#ifdef _WIN32
        // _dupenv_s, not getenv: MSVC warns about getenv, and O3DE builds with warnings as errors.
        char* v = nullptr;
        size_t n = 0;
        std::string out = (_dupenv_s(&v, &n, name) == 0 && v) ? v : fallback;
        std::free(v);
        return out;
#else
        const char* v = std::getenv(name);
        return v ? v : fallback;
#endif
    }

    // A JSON string, as the player list and the result file need.
    static std::string Quote(const std::string& s)
    {
        std::string out = "\"";
        for (unsigned char c : s)
        {
            if (c == '"' || c == '\\') { out += '\\'; out += char(c); }
            else if (c < 0x20) { char b[8]; std::snprintf(b, sizeof b, "\\u%04x", c); out += b; }
            else out += char(c);
        }
        return out + "\"";
    }

    // The value of a top-level string field, enough for the allocation's "external_id".
    static std::string Field(const std::string& json, const std::string& name)
    {
        auto k = json.find("\"" + name + "\"");
        if (k == std::string::npos) return "";
        auto q = json.find('"', json.find(':', k) + 1);
        auto e = json.find('"', q + 1);
        return q == std::string::npos || e == std::string::npos ? "" : json.substr(q + 1, e - q - 1);
    }

    LabGame::~LabGame()
    {
        m_quit = true;
        if (m_api.joinable()) m_api.join();
        if (m_sock != -1) CloseSock(Sock(m_sock));
    }

    bool LabGame::Start()
    {
#ifdef _WIN32
        WSADATA wsa;
        WSAStartup(MAKEWORD(2, 2), &wsa);
#endif
        m_gsId = Env("GS_ID", "local");
        m_token = Env("GS_TOKEN");
        // Linux images listen on the container port; Windows builds bind the public port itself.
        int port = std::atoi(Env("GS_PORT_GAME_INTERNAL").c_str());
        if (port <= 0) port = std::atoi(Env("GS_PORT_GAME").c_str());
        if (port <= 0) port = 7777;
        Sock s = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
        sockaddr_in a{};
        a.sin_family = AF_INET;
        a.sin_port = htons(uint16_t(port));
        a.sin_addr.s_addr = htonl(INADDR_ANY);
        if (s == NoSock || bind(s, reinterpret_cast<sockaddr*>(&a), sizeof a) != 0)
        {
            std::printf("bind game port %d failed\n", port);
            return false;
        }
#ifdef _WIN32
        u_long nb = 1;
        ioctlsocket(s, FIONBIO, &nb);
#else
        fcntl(s, F_SETFL, fcntl(s, F_GETFL, 0) | O_NONBLOCK);
#endif
        m_sock = (long long)s;
        std::printf("listening on udp %d; players connect to %s:%s\n", port, Env("GS_PUBLIC_IP").c_str(), Env("GS_PORT_GAME").c_str());
        std::fflush(stdout);
        // GS_LOCAL_API_URL (or its older name GS_AGENT_URL) is http://host:port.
        std::string url = Env("GS_LOCAL_API_URL");
        if (url.empty())
            url = Env("GS_AGENT_URL");
        if (url.rfind("http://", 0) == 0)
        {
            std::string hp = url.substr(7);
            hp = hp.substr(0, hp.find('/'));
            auto colon = hp.rfind(':');
            m_agentHost = colon == std::string::npos ? hp : hp.substr(0, colon);
            m_agentPort = colon == std::string::npos ? 80 : std::atoi(hp.c_str() + colon + 1);
        }
        if (m_agentHost.empty())
        {
            std::printf("%s\n", ReadyLine);
            std::fflush(stdout);
            return true;
        }
        m_api = std::thread([this] { Run(); });
        return true;
    }

    // One call to the local API over HTTP/1.0: the answer comes whole and the connection closes after it.
    LabGame::Reply LabGame::Call(const std::string& method, const std::string& path, const std::string& body, int timeoutSec)
    {
        Reply r;
        addrinfo hints{}, *res = nullptr;
        hints.ai_family = AF_INET;
        hints.ai_socktype = SOCK_STREAM;
        if (getaddrinfo(m_agentHost.c_str(), std::to_string(m_agentPort).c_str(), &hints, &res) != 0 || !res) return r;
        Sock c = socket(res->ai_family, res->ai_socktype, res->ai_protocol);
#ifdef _WIN32
        DWORD tv = DWORD(timeoutSec * 1000);
#else
        timeval tv{ timeoutSec, 0 };
#endif
        setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&tv), sizeof tv);
        if (c == NoSock || connect(c, res->ai_addr, int(res->ai_addrlen)) != 0)
        {
            freeaddrinfo(res);
            if (c != NoSock) CloseSock(c);
            return r;
        }
        freeaddrinfo(res);
        std::string req = method + " " + path + " HTTP/1.0\r\nHost: " + m_agentHost + "\r\nAuthorization: Bearer " + m_token +
            "\r\nContent-Length: " + std::to_string(body.size()) + "\r\n" +
            (body.empty() ? "" : "Content-Type: application/json\r\n") + "\r\n" + body;
        send(c, req.data(), int(req.size()), 0);
        std::string reply;
        char buf[4096];
        for (int n; (n = int(recv(c, buf, sizeof buf, 0))) > 0;) reply.append(buf, size_t(n));
        CloseSock(c);
        if (reply.rfind("HTTP/1.", 0) == 0 && reply.size() > 12) r.status = std::atoi(reply.c_str() + 9);
        auto b = reply.find("\r\n\r\n");
        if (b != std::string::npos) r.body = reply.substr(b + 4);
        return r;
    }

    void LabGame::Run()
    {
        // A fresh start is allocated before it starts: read the match now, before saying ready, so it is set up before
        // the first player arrives. A ready server (pre-started) has none yet (204) and waits for it after ready.
        Reply first = Call("GET", "/v0/self/allocation?wait=0s");
        auto setAllocation = [this](const Reply& a) {
            m_match = Field(a.body, "external_id");
            m_hasAllocation = true;
            std::printf("allocated: match \"%s\"\n", m_match.c_str()); // setting names and values stay out of the log
            std::fflush(stdout);
        };
        if (first.status == 200) setAllocation(first);
        // A version can use this exact line as its ready check instead of the call below.
        std::printf("%s\n", ReadyLine);
        std::fflush(stdout);
        Reply ready = Call("POST", "/v0/self/ready");
        if (ready.status < 200 || ready.status >= 300)
        {
            std::printf("say ready: %d %s\n", ready.status, ready.body.c_str());
            m_done = true;
            return;
        }
        // Then one loop: the newest player list when it changed, the match while there is none, and the shutdown notice
        // (short waits, so a player change goes out within a second).
        while (!m_done && !m_quit)
        {
            bool report = false;
            {
                std::lock_guard<std::mutex> lock(m_mu);
                report = m_reportPending;
                m_reportPending = false;
            }
            if (report) ReportPlayers();
            if (m_ending)
            {
                // The last player left: the empty list has gone out above, now the match's own end.
                Finish("match_over", true);
                return;
            }
            if (!m_hasAllocation)
            {
                Reply a = Call("GET", "/v0/self/allocation?wait=1s");
                if (a.status == 200) setAllocation(a);
            }
            Reply s = Call("GET", "/v0/self/shutdown?wait=1s");
            if (s.status == 200)
            {
                std::printf("stop asked; saving and leaving\n");
                Finish("stop_asked", false);
                return;
            }
            if (s.status == 0) std::this_thread::sleep_for(std::chrono::seconds(1));
        }
    }

    void LabGame::ReportPlayers()
    {
        std::string ids;
        {
            std::lock_guard<std::mutex> lock(m_mu);
            for (const auto& p : m_players) ids += (ids.empty() ? "" : ",") + Quote(p);
        }
        Call("PUT", "/v0/self/players", "{\"ids\":[" + ids + "]}");
    }

    void LabGame::Poll()
    {
        if (m_sock == -1 || m_ending) return;
        char buf[512];
        for (;;)
        {
            sockaddr_in from{};
            socklen_t len = sizeof from;
            int n = int(recvfrom(Sock(m_sock), buf, sizeof buf - 1, 0, reinterpret_cast<sockaddr*>(&from), &len));
            if (n <= 0) return;
            std::string line(buf, size_t(n));
            while (!line.empty() && (line.back() == '\n' || line.back() == '\r' || line.back() == ' ')) line.pop_back();
            std::string reply = Handle(line);
            if (!reply.empty())
                sendto(Sock(m_sock), reply.data(), int(reply.size()), 0, reinterpret_cast<sockaddr*>(&from), len);
        }
    }

    // Runs one command from a player.
    std::string LabGame::Handle(const std::string& line)
    {
        auto sp = line.find(' ');
        std::string cmd = line.substr(0, sp), who = sp == std::string::npos ? "" : line.substr(sp + 1);
        if (cmd == "ping") return "pong";
        std::lock_guard<std::mutex> lock(m_mu);
        if (cmd == "join" && !who.empty())
        {
            bool bad = who.size() > 128;
            for (unsigned char c : who) bad |= c < 0x20 || c == 0x7f;
            // The platform refuses a player list holding such a name, so it never joins; the others keep counting.
            if (bad) return "refused: a name is 1 to 128 characters with no control characters";
            if (!m_players.insert(who).second) return "already in " + m_gsId;
            m_joined = true;
            m_peak = (std::max)(m_peak, int(m_players.size())); // in parentheses: windows.h may define max
            m_reportPending = true;
            std::printf("%s joined (%d playing)\n", who.c_str(), int(m_players.size()));
            std::fflush(stdout);
            return "welcome " + who + " to " + m_gsId;
        }
        if (cmd == "leave" && !who.empty())
        {
            if (m_players.erase(who) == 0) return "not in " + m_gsId;
            m_reportPending = true;
            std::printf("%s left (%d playing)\n", who.c_str(), int(m_players.size()));
            std::fflush(stdout);
            if (m_joined && m_players.empty())
            {
                m_ending = true; // the API thread sends the empty list, then ends the match
                if (m_agentHost.empty()) m_done = true; // no platform: just leave
            }
            return "bye " + who;
        }
        return "unknown command; try join <name>, leave <name> or ping";
    }

    // Ends the match: the result is saved, and if the game server decided to end (rather than being asked to stop) it
    // tells the platform, which is the same as exiting with code 0.
    void LabGame::Finish(const std::string& reason, bool selfStop)
    {
        m_ending = true;
        auto seconds = std::chrono::duration_cast<std::chrono::seconds>(std::chrono::steady_clock::now() - m_started).count();
        std::printf("match over: %s after %llds, %d players at most\n", reason.c_str(), (long long)seconds, m_peak);
        std::fflush(stdout);
        WriteResult(reason, seconds);
        if (selfStop && !m_agentHost.empty()) Call("POST", "/v0/self/stop", "{\"reason\":" + Quote(reason) + "}");
        m_done = true;
    }

    // match-result.json in GS_ARTIFACT_DIR is collected after the game server exits.
    void LabGame::WriteResult(const std::string& reason, long long seconds)
    {
        std::string dir = Env("GS_ARTIFACT_DIR");
        if (dir.empty()) return;
        std::ofstream f(dir + "/match-result.json");
        f << "{\"game_server_id\":" << Quote(m_gsId) << ",\"reason\":" << Quote(reason) << ",\"peak_players\":" << m_peak
          << ",\"seconds\":" << seconds;
        if (m_hasAllocation) f << ",\"match\":" << Quote(m_match);
        f << "}\n";
    }
}
