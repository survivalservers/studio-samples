#include "GameServerCore.h"

#include <algorithm>
#include <cmath>
#include <cstdio>

namespace SurvivalServersCore
{
	namespace
	{
		int ParsePort(const char* Name, const std::string& Value, std::string& Error)
		{
			if (Value.empty()) return 0;
			int Port = 0;
			for (char C : Value)
			{
				if (C < '0' || C > '9' || Port > 65535) break;
				Port = Port * 10 + (C - '0');
			}
			if (Value.find_first_not_of("0123456789") != std::string::npos || Port > 65535)
			{
				Error = std::string(Name) + "=" + Value + " is not a port";
				return 0;
			}
			return Port;
		}
	}

	Config Config::FromEnv(const std::function<std::string(const char*)>& Getenv, std::string& Error)
	{
		Error.clear();
		Config C;
		if (!Getenv("GS_ID").empty()) C.Id = Getenv("GS_ID");
		C.PublicIp = Getenv("GS_PUBLIC_IP");
		C.AgentUrl = Getenv("GS_LOCAL_API_URL");
		if (C.AgentUrl.empty())
			C.AgentUrl = Getenv("GS_AGENT_URL");
		while (!C.AgentUrl.empty() && C.AgentUrl.back() == '/') C.AgentUrl.pop_back();
		C.Token = Getenv("GS_TOKEN");
		C.AllocationFile = Getenv("GS_ALLOCATION_FILE");
		C.ArtifactDir = Getenv("GS_ARTIFACT_DIR");
		C.Kind = Getenv("GS_KIND");
		C.PublicPort = ParsePort("GS_PORT_GAME", Getenv("GS_PORT_GAME"), Error);
		const int Internal = ParsePort("GS_PORT_GAME_INTERNAL", Getenv("GS_PORT_GAME_INTERNAL"), Error);
		C.ListenPort = Internal != 0 ? Internal : C.PublicPort != 0 ? C.PublicPort : 7777;
		return C;
	}

	bool Match::Join(const std::string& PlayerId)
	{
		if (!Players.insert(PlayerId).second) return false;
		bJoined = true;
		if (Count() > PeakCount) PeakCount = Count();
		return true;
	}

	bool Match::Leave(const std::string& PlayerId)
	{
		return Players.erase(PlayerId) > 0;
	}

	PollNext AfterLongPoll(int Status)
	{
		if (Status == 200) return PollNext::Use;
		if (Status == 204) return PollNext::Again;
		return PollNext::Pause;
	}

	std::string PlayerIdProblem(const std::string& PlayerId)
	{
		if (PlayerId.empty()) return "is empty";
		size_t Count = 0;
		for (size_t I = 0; I < PlayerId.size(); ++Count)
		{
			const unsigned char B = static_cast<unsigned char>(PlayerId[I]);
			const int Len = B < 0x80 ? 1 : (B & 0xE0) == 0xC0 ? 2 : (B & 0xF0) == 0xE0 ? 3 : (B & 0xF8) == 0xF0 ? 4 : 0;
			if (Len == 0 || I + Len > PlayerId.size()) return "is not UTF-8 text";
			unsigned long Cp = Len == 1 ? B : Len == 2 ? (B & 0x1F) : Len == 3 ? (B & 0x0F) : (B & 0x07);
			for (int K = 1; K < Len; ++K)
			{
				const unsigned char N = static_cast<unsigned char>(PlayerId[I + K]);
				if ((N & 0xC0) != 0x80) return "is not UTF-8 text";
				Cp = Cp << 6 | (N & 0x3F);
			}
			if ((Len == 2 && Cp < 0x80) || (Len == 3 && Cp < 0x800) || (Len == 4 && (Cp < 0x10000 || Cp > 0x10FFFF)) || (Cp >= 0xD800 && Cp <= 0xDFFF))
				return "is not UTF-8 text";
			if (Cp < 0x20 || (Cp >= 0x7F && Cp <= 0x9F)) return "has a control character";
			I += Len;
		}
		return Count > 128 ? "is longer than 128 characters" : "";
	}

	std::string JsonString(const std::string& Value)
	{
		std::string Out = "\"";
		for (unsigned char C : Value)
		{
			switch (C)
			{
			case '"': Out += "\\\""; break;
			case '\\': Out += "\\\\"; break;
			case '\n': Out += "\\n"; break;
			case '\r': Out += "\\r"; break;
			case '\t': Out += "\\t"; break;
			default:
				if (C < 0x20)
				{
					char Buf[8];
					std::snprintf(Buf, sizeof Buf, "\\u%04x", C);
					Out += Buf;
				}
				else Out += static_cast<char>(C);
			}
		}
		return Out + "\"";
	}

	std::string PlayersBody(const std::vector<std::string>& Ids)
	{
		std::string Out = "{\"ids\":[";
		for (size_t I = 0; I < Ids.size(); ++I) Out += (I ? "," : "") + JsonString(Ids[I]);
		return Out + "]}";
	}

	std::string CountBody(int Count)
	{
		return "{\"count\":" + std::to_string(Count < 0 ? 0 : Count) + "}";
	}

	std::string LabelBody(const std::string& Name, const std::string* Value)
	{
		return "{" + JsonString(Name) + ":" + (Value ? JsonString(*Value) : std::string("null")) + "}";
	}

	namespace
	{
		// A JSON number with up to three decimals and no trailing zeros: 2.1, 6, 0.25.
		std::string Number(double Value)
		{
			if (!(Value >= 0)) Value = 0; // NaN and negatives
			char Buf[32];
			std::snprintf(Buf, sizeof Buf, "%.3f", Value);
			std::string Out(Buf);
			Out.erase(Out.find_last_not_of('0') + 1);
			if (!Out.empty() && Out.back() == '.') Out.pop_back();
			return Out;
		}
	}

	std::string TickBody(double P50Ms, double P99Ms, int Hz)
	{
		std::string Out = "{\"p50_ms\":" + Number(P50Ms) + ",\"p99_ms\":" + Number(P99Ms);
		if (Hz > 0) Out += ",\"hz\":" + std::to_string(Hz);
		return Out + "}";
	}

	void FrameTimes::Add(double WorkMs)
	{
		// A server that never sleeps reports its whole frame; a clock step backwards is not a negative frame. At most
		// 10,000 frames a second are kept, far above any server's tick rate.
		if (!(WorkMs >= 0)) WorkMs = 0;
		if (Samples.size() < 10000) Samples.push_back(WorkMs);
	}

	void FrameTimes::Take(double& P50Ms, double& P99Ms)
	{
		P50Ms = P99Ms = 0;
		if (Samples.empty()) return;
		std::sort(Samples.begin(), Samples.end());
		const auto Rank = [this](double Q)
		{
			const size_t N = Samples.size();
			size_t I = static_cast<size_t>(std::ceil(Q * static_cast<double>(N)));
			return Samples[I == 0 ? 0 : (I > N ? N - 1 : I - 1)];
		};
		P50Ms = Rank(0.50);
		P99Ms = Rank(0.99);
		Samples.clear();
	}

	int ClampWait(int Seconds)
	{
		return Seconds < 0 ? 0 : (Seconds > MaxWaitSeconds ? MaxWaitSeconds : Seconds);
	}

	std::string StopBody(const std::string& Reason)
	{
		return "{\"reason\":" + JsonString(Reason) + "}";
	}

	std::string SavedBody(const std::string& Keep)
	{
		return Keep.empty() ? "{}" : "{\"keep\":" + JsonString(Keep) + "}";
	}

	std::string ResultJson(const std::string& GameServerId, const std::string& Reason, int PeakPlayers, int Seconds, const std::string& MatchId)
	{
		std::string Out = "{\n  \"game_server_id\": " + JsonString(GameServerId) + ",\n  \"reason\": " + JsonString(Reason) +
			",\n  \"peak_players\": " + std::to_string(PeakPlayers) + ",\n  \"seconds\": " + std::to_string(Seconds);
		if (!MatchId.empty()) Out += ",\n  \"match\": " + JsonString(MatchId);
		return Out + "\n}\n";
	}

	std::string LongPollUrl(const Config& Cfg, const std::string& What, int WaitSeconds)
	{
		return Cfg.AgentUrl + "/v0/self/" + What + "?wait=" + std::to_string(ClampWait(WaitSeconds)) + "s";
	}
}
