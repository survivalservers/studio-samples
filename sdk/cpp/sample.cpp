// The smallest game server with the C++ layer; see ../c/sample/sample.c for what it shows.
#include <chrono>
#include <iostream>

#include "gs_sdk.hpp"

int main() {
	using namespace std::chrono_literals;
	try {
		auto gs = gs::Client::FromEnvironment();
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
