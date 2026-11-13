// Tests for the plugin's plain C++ rules (no engine needed):  ./run.sh
#include "../SurvivalServersStudio/Source/SurvivalServersStudio/Private/Core/GameServerCore.h"

#include <cmath>
#include <cstdio>
#include <map>
#include <string>

static int Failures = 0;
#define CHECK(Cond) do { if (!(Cond)) { std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #Cond); ++Failures; } } while (0)
#define CHECK_EQ(A, B) do { auto _a = (A); auto _b = (B); if (!(_a == _b)) { std::printf("FAIL %s:%d: %s == %s\n", __FILE__, __LINE__, #A, #B); ++Failures; } } while (0)

static std::function<std::string(const char*)> Env(std::map<std::string, std::string> Vars)
{
	return [Vars](const char* Name) { auto It = Vars.find(Name); return It == Vars.end() ? std::string() : It->second; };
}

static void ConfigFromEnvironment()
{
	std::string Error;
	auto Local = SurvivalServersCore::Config::FromEnv(Env({}), Error);
	CHECK(Error.empty());
	CHECK_EQ(Local.Id, std::string("local"));
	CHECK_EQ(Local.ListenPort, 7777);
	CHECK(!Local.HasAgent());

	auto Windows = SurvivalServersCore::Config::FromEnv(Env({{"GS_ID", "gs_1"}, {"GS_PORT_GAME", "31042"}, {"GS_LOCAL_API_URL", "http://127.0.0.1:7600/"}, {"GS_TOKEN", "t"}}), Error);
	CHECK_EQ(Windows.Id, std::string("gs_1"));
	CHECK_EQ(Windows.ListenPort, 31042);
	CHECK_EQ(Windows.PublicPort, 31042);
	CHECK_EQ(Windows.AgentUrl, std::string("http://127.0.0.1:7600"));
	CHECK(Windows.HasAgent());

	auto Linux = SurvivalServersCore::Config::FromEnv(Env({{"GS_PORT_GAME", "31042"}, {"GS_PORT_GAME_INTERNAL", "7777"}}), Error);
	CHECK(Error.empty());
	CHECK_EQ(Linux.ListenPort, 7777);
	CHECK_EQ(Linux.PublicPort, 31042);

	for (const char* Bad : {"seven", "70000", "-1", "99999999999999999999"})
	{
		SurvivalServersCore::Config::FromEnv(Env({{"GS_PORT_GAME", Bad}}), Error);
		CHECK(Error == std::string("GS_PORT_GAME=") + Bad + " is not a port");
	}
}

// The older names (GS_KIND=world, and the local API's address in GS_AGENT_URL instead of GS_LOCAL_API_URL) give the
// same settings as today's.
static void OlderHostNamesChangeNothing()
{
	std::map<std::string, std::string> Common{{"GS_ID", "gs_1"}, {"GS_PORT_GAME", "31042"}, {"GS_TOKEN", "t"}};
	auto Current = Common, Older = Common;
	Current.insert({{"GS_KIND", "persistent_server"}, {"GS_LOCAL_API_URL", "http://127.0.0.1:7600"}});
	Older.insert({{"GS_KIND", "world"}, {"GS_AGENT_URL", "http://127.0.0.1:7600"}});
	std::string ErrorA, ErrorB;
	auto A = SurvivalServersCore::Config::FromEnv(Env(Current), ErrorA);
	auto B = SurvivalServersCore::Config::FromEnv(Env(Older), ErrorB);
	CHECK(ErrorA.empty() && ErrorB.empty());
	CHECK(A.Id == B.Id && A.PublicIp == B.PublicIp && A.PublicPort == B.PublicPort && A.ListenPort == B.ListenPort &&
		A.AgentUrl == B.AgentUrl && A.Token == B.Token && A.AllocationFile == B.AllocationFile && A.ArtifactDir == B.ArtifactDir);
	CHECK_EQ(B.Id, std::string("gs_1"));
	CHECK_EQ(B.ListenPort, 31042);
	CHECK_EQ(B.AgentUrl, std::string("http://127.0.0.1:7600"));
}

static void MatchEndsWhenEveryoneWhoJoinedHasLeft()
{
	SurvivalServersCore::Match M;
	CHECK(!M.Over());
	CHECK(M.Join("ana"));
	CHECK(!M.Join("ana"));
	CHECK(M.Join("bo"));
	CHECK_EQ(M.Peak(), 2);
	CHECK(M.Ids() == (std::vector<std::string>{"ana", "bo"}));
	CHECK(M.Leave("ana"));
	CHECK(!M.Leave("ana"));
	CHECK(!M.Over());
	CHECK(M.Leave("bo"));
	CHECK(M.Over());
	CHECK_EQ(M.Count(), 0);
	CHECK_EQ(M.Peak(), 2);
}

static void PlayerIdsWithControlCharactersAreRefused()
{
	CHECK(SurvivalServersCore::PlayerIdProblem("ana").empty());
	CHECK(SurvivalServersCore::PlayerIdProblem("Zo\xc3\xab \xf0\x9f\x8e\xae").empty()); // "Zoë 🎮"
	CHECK(SurvivalServersCore::PlayerIdProblem(std::string("bad\0name", 8)) == "has a control character");
	CHECK(SurvivalServersCore::PlayerIdProblem("tab\there") == "has a control character");
	CHECK(SurvivalServersCore::PlayerIdProblem("c1\xc2\x85") == "has a control character");
	CHECK(SurvivalServersCore::PlayerIdProblem("\xff") == "is not UTF-8 text");
	CHECK(SurvivalServersCore::PlayerIdProblem("") == "is empty");
	std::string Long;
	for (int I = 0; I < 128; ++I) Long += "\xc3\xa9";
	CHECK(SurvivalServersCore::PlayerIdProblem(Long).empty());
	CHECK(SurvivalServersCore::PlayerIdProblem(Long + "x") == "is longer than 128 characters");
}

static void LongPollAnswers()
{
	CHECK(SurvivalServersCore::AfterLongPoll(200) == SurvivalServersCore::PollNext::Use);
	CHECK(SurvivalServersCore::AfterLongPoll(204) == SurvivalServersCore::PollNext::Again);
	CHECK(SurvivalServersCore::AfterLongPoll(0) == SurvivalServersCore::PollNext::Pause);
	CHECK(SurvivalServersCore::AfterLongPoll(429) == SurvivalServersCore::PollNext::Pause);
	CHECK(SurvivalServersCore::AfterLongPoll(500) == SurvivalServersCore::PollNext::Pause);
}

static void RequestBodiesAndResult()
{
	CHECK_EQ(SurvivalServersCore::PlayersBody({}), std::string("{\"ids\":[]}"));
	CHECK_EQ(SurvivalServersCore::PlayersBody({"p_1", "p_2"}), std::string("{\"ids\":[\"p_1\",\"p_2\"]}"));
	CHECK_EQ(SurvivalServersCore::StopBody("match_over"), std::string("{\"reason\":\"match_over\"}"));
	CHECK_EQ(SurvivalServersCore::SavedBody(""), std::string("{}"));
	CHECK_EQ(SurvivalServersCore::SavedBody("Season 3 end"), std::string("{\"keep\":\"Season 3 end\"}"));
	CHECK_EQ(SurvivalServersCore::JsonString("a\"b\\c\nd\x01" "é"), std::string("\"a\\\"b\\\\c\\nd\\u0001é\""));
	CHECK_EQ(SurvivalServersCore::ResultJson("gs_1", "match_over", 2, 61, "88231"),
		std::string("{\n  \"game_server_id\": \"gs_1\",\n  \"reason\": \"match_over\",\n  \"peak_players\": 2,\n  \"seconds\": 61,\n  \"match\": \"88231\"\n}\n"));
	CHECK(SurvivalServersCore::ResultJson("gs_1", "stop_asked", 0, 5, "").find("match\"") == std::string::npos);

	std::string Error;
	auto Cfg = SurvivalServersCore::Config::FromEnv(Env({{"GS_LOCAL_API_URL", "http://127.0.0.1:7600"}}), Error);
	CHECK_EQ(SurvivalServersCore::LongPollUrl(Cfg, "allocation", 30), std::string("http://127.0.0.1:7600/v0/self/allocation?wait=30s"));
	CHECK_EQ(SurvivalServersCore::LongPollUrl(Cfg, "shutdown", 300), std::string("http://127.0.0.1:7600/v0/self/shutdown?wait=60s"));
	CHECK_EQ(SurvivalServersCore::ClampWait(-5), 0);
}

// The calls added to match the custom-engine SDK's names: count, labels and frame times.
static void SdkCallBodies()
{
	CHECK_EQ(SurvivalServersCore::CountBody(3), std::string("{\"count\":3}"));
	CHECK_EQ(SurvivalServersCore::CountBody(-1), std::string("{\"count\":0}"));
	const std::string Desert = "desert";
	CHECK_EQ(SurvivalServersCore::LabelBody("map", &Desert), std::string("{\"map\":\"desert\"}"));
	CHECK_EQ(SurvivalServersCore::LabelBody("queue", nullptr), std::string("{\"queue\":null}"));
	CHECK_EQ(SurvivalServersCore::TickBody(2.1, 3.9, 128), std::string("{\"p50_ms\":2.1,\"p99_ms\":3.9,\"hz\":128}"));
	CHECK_EQ(SurvivalServersCore::TickBody(6.0, 0.25, 0), std::string("{\"p50_ms\":6,\"p99_ms\":0.25}"));
}

static void RunKind()
{
	std::string Error;
	CHECK(!SurvivalServersCore::Config::FromEnv(Env({}), Error).IsPersistent());
	CHECK(!SurvivalServersCore::Config::FromEnv(Env({{"GS_KIND", "match"}}), Error).IsPersistent());
	auto P = SurvivalServersCore::Config::FromEnv(Env({{"GS_KIND", "persistent_server"}}), Error);
	CHECK(P.IsPersistent());
	CHECK_EQ(P.Kind, std::string("persistent_server"));
}

static void FrameTimesPerSecond()
{
	SurvivalServersCore::FrameTimes F;
	double P50 = -1, P99 = -1;
	CHECK(F.Empty());
	F.Take(P50, P99);
	CHECK(P50 == 0 && P99 == 0);
	// 100 frames of 1..100 ms: the median is 50, the 99th percentile 99 (nearest rank).
	for (int I = 100; I >= 1; --I) F.Add(I);
	F.Take(P50, P99);
	CHECK(P50 == 50 && P99 == 99);
	CHECK(F.Empty());
	// One frame: both are that frame. A negative or NaN time counts as 0.
	F.Add(4.5);
	F.Take(P50, P99);
	CHECK(P50 == 4.5 && P99 == 4.5);
	F.Add(-3);
	F.Add(std::nan(""));
	F.Take(P50, P99);
	CHECK(P50 == 0 && P99 == 0);
	// The body the plugin sends with those values.
	CHECK_EQ(SurvivalServersCore::TickBody(50, 99, 30), std::string("{\"p50_ms\":50,\"p99_ms\":99,\"hz\":30}"));
}

int main()
{
	ConfigFromEnvironment();
	OlderHostNamesChangeNothing();
	MatchEndsWhenEveryoneWhoJoinedHasLeft();
	PlayerIdsWithControlCharactersAreRefused();
	LongPollAnswers();
	RequestBodiesAndResult();
	SdkCallBodies();
	RunKind();
	FrameTimesPerSecond();
	if (Failures) { std::printf("%d check(s) failed\n", Failures); return 1; }
	std::printf("plugin core: all checks passed\n");
	return 0;
}
