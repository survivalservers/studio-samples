// gs_sdk for C++17: a thin, header-only layer over the C library (gs_sdk.h), with exceptions and std types.
// Licensed under the Apache License, Version 2.0.
#pragma once

#include <chrono>
#include <cstdint>
#include <map>
#include <optional>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

#include "gs_sdk.h"

namespace gs {

// Error is a failed call: its code and the agent's or the library's reason.
class Error : public std::runtime_error {
public:
	Error(gs_result code, const std::string& what) : std::runtime_error(what), code_(code) {}
	gs_result code() const { return code_; }

private:
	gs_result code_;
};

// Allocation is the game server's match: its IDs, settings, labels and command-line arguments.
struct Allocation {
	std::string game_server_id;
	std::string external_id;
	std::map<std::string, std::string> env;
	std::map<std::string, std::string> labels;
	std::vector<std::string> args;
};

// Shutdown is a shutdown notice: save and exit before the deadline.
struct Shutdown {
	std::int64_t deadline_unix_ms = 0;
	std::string reason;
};

class Client {
public:
	// FromEnvironment connects as the game server the machine started (GS_LOCAL_API_URL, GS_TOKEN).
	static Client FromEnvironment() {
		gs_client* c = nullptr;
		gs_result r = gs_open(&c);
		if (r != GS_OK) throw Error(r, r == GS_ERR_NO_AGENT ? "no local API here (GS_LOCAL_API_URL and GS_TOKEN are not set)" : "gs_open failed");
		return Client(c);
	}
	Client(const std::string& agent_url, const std::string& token) {
		gs_result r = gs_open_with(agent_url.c_str(), token.c_str(), &c_);
		if (r != GS_OK) throw Error(r, "gs_open_with failed");
	}
	Client(Client&& o) noexcept : c_(std::exchange(o.c_, nullptr)) {}
	Client& operator=(Client&& o) noexcept {
		if (this != &o) {
			gs_close(c_);
			c_ = std::exchange(o.c_, nullptr);
		}
		return *this;
	}
	Client(const Client&) = delete;
	Client& operator=(const Client&) = delete;
	~Client() { gs_close(c_); }

	void Ready() { check(gs_ready(c_)); }
	void Health() { check(gs_health(c_)); }
	void Stop(const std::string& reason) { check(gs_stop(c_, reason.c_str())); }
	// A persistent server just finished a save: it is backed up at once, and with keep, kept under that name. An agent
	// older than this call is not an error: the folder watcher backs saves up without it.
	void Saved(const std::string& keep = "") {
		gs_result r = gs_saved(c_, keep.empty() ? nullptr : keep.c_str());
		if (r != GS_ERR_UNSUPPORTED) check(r);
	}

	// WaitAllocation waits up to wait for the match; empty when there is none yet.
	std::optional<Allocation> WaitAllocation(std::chrono::milliseconds wait) {
		gs_allocation* a = nullptr;
		gs_result r = gs_wait_allocation(c_, static_cast<int>(wait.count()), &a);
		if (r == GS_ERR_NONE_YET) return std::nullopt;
		check(r);
		Allocation out;
		out.game_server_id = gs_allocation_game_server_id(a);
		out.external_id = gs_allocation_external_id(a);
		for (std::size_t i = 0; gs_allocation_arg(a, i); i++) out.args.push_back(gs_allocation_arg(a, i));
		for (std::size_t i = 0; gs_allocation_env_name(a, i); i++) out.env[gs_allocation_env_name(a, i)] = gs_allocation_env_value(a, i);
		for (std::size_t i = 0; gs_allocation_label_name(a, i); i++) out.labels[gs_allocation_label_name(a, i)] = gs_allocation_label_value(a, i);
		gs_allocation_free(a);
		return out;
	}

	// WaitShutdown waits up to wait for a shutdown notice; empty when there is none.
	std::optional<Shutdown> WaitShutdown(std::chrono::milliseconds wait) {
		Shutdown s;
		char reason[128];
		gs_result r = gs_wait_shutdown(c_, static_cast<int>(wait.count()), &s.deadline_unix_ms, reason, sizeof reason);
		if (r == GS_ERR_NONE_YET) return std::nullopt;
		check(r);
		s.reason = reason;
		return s;
	}

	// An ID is 1 to 128 characters with no control characters; any other (often a name a player chose) is refused
	// (GS_ERR_ARGUMENT) and never joins the list.
	void PlayerConnect(const std::string& id) {
		if (id.find('\0') != std::string::npos) throw Error(GS_ERR_ARGUMENT, "the player ID has a control character (1 to 128 characters, no control characters)");
		check(gs_player_connect(c_, id.c_str()));
	}
	void PlayerDisconnect(const std::string& id) { check(gs_player_disconnect(c_, id.c_str())); }
	void SetPlayerCount(int count) { check(gs_set_player_count(c_, count)); }
	void SetLabel(const std::string& name, const std::string& value) { check(gs_set_label(c_, name.c_str(), value.c_str())); }
	void RemoveLabel(const std::string& name) { check(gs_set_label(c_, name.c_str(), nullptr)); }
	void Tick(double p50_ms, double p99_ms, int hz = 0) { check(gs_tick(c_, p50_ms, p99_ms, hz)); }

private:
	explicit Client(gs_client* c) : c_(c) {}
	void check(gs_result r) {
		if (r != GS_OK) throw Error(r, gs_last_error(c_));
	}
	gs_client* c_ = nullptr;
};

}  // namespace gs
