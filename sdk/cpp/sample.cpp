// The smallest game server with the C++ layer; see ../c/sample/sample.c for what it shows.
#include <atomic>
#include <chrono>
#include <cstdlib>
#include <iostream>
#include <thread>

#include "gs_sdk.hpp"

// With GS_SAMPLE_BACKGROUND set, the library's background helper sends health and calls the two lambdas once each.
static int background(gs::Client& gs) {
	using namespace std::chrono_literals;
	std::atomic<bool> matched{false}, stopped{false};
	gs::Shutdown sd;
	gs.StartBackground(
	    200ms,
	    [&](const gs::Allocation& a) {
		    auto map = a.env.find("MAP");
		    std::cout << "match " << a.external_id << " map=" << (map == a.env.end() ? "-" : map->second) << std::endl;
		    matched = true;
	    },
	    [&](const gs::Shutdown& s) {
		    sd = s;
		    stopped = true;
	    });
	gs.Ready();
	for (int i = 0; !matched && i < 1500; i++) std::this_thread::sleep_for(20ms);
	gs.PlayerConnect("p1");
	gs.SetLabel("mode", "ffa");
	std::this_thread::sleep_for(1500ms);  // the helper sends health meanwhile
	gs.Stop("match_over");
	for (int i = 0; !stopped && i < 1500; i++) std::this_thread::sleep_for(20ms);
	std::cout << "shutdown reason=" << sd.reason << " deadline=" << (sd.deadline_unix_ms > 0 ? "set" : "none") << std::endl;
	gs.StopBackground();
	return 0;
}

int main() {
	using namespace std::chrono_literals;
	try {
		auto gs = gs::Client::FromEnvironment();
		if (std::getenv("GS_SAMPLE_BACKGROUND")) return background(gs);
		gs.Ready();
		auto match = gs.WaitAllocation(30s);
		if (!match) return 3;
		std::cout << "match " << match->external_id << " map=" << match->env["MAP"] << std::endl;
		gs.PlayerConnect("p1");
		gs.PlayerConnect("p2");
		try { // a name a player chose, with a NUL in it: refused, and p1 and p2 still count
			gs.PlayerConnect(std::string("bad\0name", 8));
			return 1;
		} catch (const gs::Error&) {
			std::cout << "refused a player ID with a control character" << std::endl;
		}
		gs.PlayerDisconnect("p2");
		gs.SetLabel("mode", "ffa");
		gs.Health();
		gs.Tick(2.5, 6.25, 64);
		gs.Stop("match_over");
		auto sd = gs.WaitShutdown(30s);
		std::cout << "shutdown reason=" << (sd ? sd->reason : "none") << " deadline=" << (sd && sd->deadline_unix_ms > 0 ? "set" : "none")
		          << std::endl;
	} catch (const gs::Error& e) {
		std::cerr << "gs: " << e.code() << " " << e.what() << std::endl;
		return 1;
	}
	return 0;
}
