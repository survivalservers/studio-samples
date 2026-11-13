// The plugin's rules in plain C++17 with no Unreal types, so they build and are tested without the engine
// (tests/core_test.cpp). The Unreal layer (USurvivalServersSubsystem) feeds them engine events and sends the calls.
#pragma once

#include <functional>
#include <set>
#include <string>
#include <vector>

namespace SurvivalServersCore
{
	// What the game server learns from its environment. Missing values fall back to a local run on port 7777.
	struct Config
	{
		std::string Id = "local";       // GS_ID
		std::string PublicIp;            // GS_PUBLIC_IP: the address players use
		int PublicPort = 0;              // GS_PORT_GAME: the port players use
		int ListenPort = 7777;           // GS_PORT_GAME_INTERNAL in a Linux image, else GS_PORT_GAME
		std::string AgentUrl;            // GS_LOCAL_API_URL (or its older name GS_AGENT_URL), without a trailing slash
		std::string Token;               // GS_TOKEN
		std::string AllocationFile;      // GS_ALLOCATION_FILE
		std::string ArtifactDir;         // GS_ARTIFACT_DIR: files here are collected after exit
		std::string Kind;                // GS_KIND: match or persistent_server (empty in a local run)

		// Unreal builds without C++ exceptions, so a bad value is reported in Error (empty when all is well).
		static Config FromEnv(const std::function<std::string(const char*)>& Getenv, std::string& Error);
		bool HasAgent() const { return !AgentUrl.empty(); }
		bool IsPersistent() const { return Kind == "persistent_server"; }
	};

	// The players in one match and when it is over: once someone has joined, the match ends when the last one leaves.
	class Match
	{
	public:
		bool Join(const std::string& PlayerId);   // false if already in
		bool Leave(const std::string& PlayerId);  // false if not in
		bool Over() const { return bJoined && Players.empty(); }
		int Count() const { return static_cast<int>(Players.size()); }
		int Peak() const { return PeakCount; }
		std::vector<std::string> Ids() const { return {Players.begin(), Players.end()}; }

	private:
		std::set<std::string> Players;
		bool bJoined = false;
		int PeakCount = 0;
	};

	// What is wrong with a player ID, or empty when nothing is: the agent's rule, 1 to 128 characters of UTF-8 with no
	// control characters (U+0000-U+001F, U+007F-U+009F). The agent refuses a player list holding such an ID whole, and
	// a player's own name can be the ID, so such an ID never joins the list.
	std::string PlayerIdProblem(const std::string& PlayerId);

	// What to do with a long-poll's answer: use it (200), ask again at once (204: the wait ran out), or wait a second
	// and ask again (anything else, including a call that did not complete, status 0).
	enum class PollNext { Use, Again, Pause };
	PollNext AfterLongPoll(int Status);

	// Request bodies and the result file, as JSON.
	std::string JsonString(const std::string& Value);
	std::string PlayersBody(const std::vector<std::string>& Ids);
	std::string CountBody(int Count);
	std::string StopBody(const std::string& Reason);
	std::string SavedBody(const std::string& Keep);
	// One label set ({"map":"desert"}) or, with no value, removed ({"queue":null}).
	std::string LabelBody(const std::string& Name, const std::string* Value);
	// Frame times for the last second; Hz 0 leaves the rate out.
	std::string TickBody(double P50Ms, double P99Ms, int Hz);
	std::string ResultJson(const std::string& GameServerId, const std::string& Reason, int PeakPlayers, int Seconds, const std::string& MatchId);

	// Work time per frame (the frame minus the time the engine slept), summed up once a second for the tick graph.
	class FrameTimes
	{
	public:
		void Add(double WorkMs);
		bool Empty() const { return Samples.empty(); }
		// The median and the 99th percentile (nearest rank) of the frames since the last call, which starts over.
		void Take(double& P50Ms, double& P99Ms);

	private:
		std::vector<double> Samples;
	};

	// The longest a long-poll waits; the local API answers "nothing yet" by then.
	constexpr int MaxWaitSeconds = 60;
	int ClampWait(int Seconds);

	// "http://127.0.0.1:7600" + "/v0/self/allocation?wait=30s" (the wait clamped to 0..60 seconds)
	std::string LongPollUrl(const Config& Cfg, const std::string& What, int WaitSeconds);
}
