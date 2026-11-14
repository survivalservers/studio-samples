/* The smallest game server with gs_sdk: ready, its match, a player, a label, health and update times, then it ends
 * itself and waits for the shutdown notice. Run it under the local test agent:
 *   survivalservers dev -warm -allocate-after 1s -env MAP=dunes -external-id m42 -stop local_api -- ./sample */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#  include <windows.h>
static void sleep_ms(int ms) { Sleep((DWORD)ms); }
#else
#  include <time.h>
static void sleep_ms(int ms) {
	struct timespec ts;
	ts.tv_sec = ms / 1000;
	ts.tv_nsec = (long)(ms % 1000) * 1000000L;
	nanosleep(&ts, NULL);
}
#endif

#include "gs_sdk.h"

/* With GS_SAMPLE_BACKGROUND set, the library's background helper sends health and calls these once each. */
static volatile int matched, stopped;
static char stop_reason[64];
static int64_t stop_deadline;

static void on_match(const gs_allocation *a, void *user) {
	(void)user;
	printf("match %s map=%s\n", gs_allocation_external_id(a), gs_allocation_env(a, "MAP") ? gs_allocation_env(a, "MAP") : "-");
	fflush(stdout);
	matched = 1;
}

static void on_stop(int64_t deadline_unix_ms, const char *reason, void *user) {
	(void)user;
	snprintf(stop_reason, sizeof stop_reason, "%s", reason);
	stop_deadline = deadline_unix_ms;
	stopped = 1;
}

#define CHECK(call)                                                                       \
	do {                                                                                  \
		gs_result r_ = (call);                                                            \
		if (r_ != GS_OK) {                                                                \
			fprintf(stderr, "%s: %d %s\n", #call, (int)r_, gs_last_error(c));            \
			return 1;                                                                     \
		}                                                                                 \
	} while (0)

int main(void) {
	gs_client *c = NULL;
	gs_allocation *a = NULL;
	char reason[64];
	int64_t deadline = 0;
	if (gs_open(&c) != GS_OK) {
		fprintf(stderr, "no local API here (GS_LOCAL_API_URL and GS_TOKEN are not set)\n");
		return 2;
	}
	if (getenv("GS_SAMPLE_BACKGROUND")) {
		int waited;
		CHECK(gs_background_start(c, 200, on_match, on_stop, NULL));
		CHECK(gs_ready(c));
		for (waited = 0; !matched && waited < 30000; waited += 20) sleep_ms(20);
		CHECK(gs_player_connect(c, "p1"));
		CHECK(gs_set_label(c, "mode", "ffa"));
		sleep_ms(1500); /* the helper sends health meanwhile */
		CHECK(gs_stop(c, "match_over"));
		for (waited = 0; !stopped && waited < 30000; waited += 20) sleep_ms(20);
		printf("shutdown reason=%s deadline=%s\n", stop_reason, stop_deadline > 0 ? "set" : "none");
		fflush(stdout);
		gs_close(c); /* stops the helper */
		return 0;
	}
	CHECK(gs_ready(c));
	CHECK(gs_wait_allocation(c, 30000, &a));
	printf("match %s map=%s\n", gs_allocation_external_id(a), gs_allocation_env(a, "MAP") ? gs_allocation_env(a, "MAP") : "-");
	gs_allocation_free(a);
	CHECK(gs_player_connect(c, "p1"));
	CHECK(gs_player_connect(c, "p2"));
	/* A name a player chose, with a control character in it: refused, and p1 and p2 still count. */
	if (gs_player_connect(c, "bad\001name") != GS_ERR_ARGUMENT) return 1;
	printf("refused a player ID with a control character\n");
	CHECK(gs_player_disconnect(c, "p2"));
	CHECK(gs_set_label(c, "mode", "ffa"));
	CHECK(gs_health(c));
	CHECK(gs_tick(c, 2.5, 6.25, 64));
	CHECK(gs_stop(c, "match_over"));
	CHECK(gs_wait_shutdown(c, 30000, &deadline, reason, sizeof reason));
	printf("shutdown reason=%s deadline=%s\n", reason, deadline > 0 ? "set" : "none");
	fflush(stdout);
	gs_close(c);
	return 0;
}
