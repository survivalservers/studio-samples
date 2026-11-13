/* The smallest game server with gs_sdk: ready, its match, a player, a label, health and update times, then it ends
 * itself and waits for the shutdown notice. Run it under the local test agent:
 *   survivalservers dev -warm -allocate-after 1s -env MAP=dunes -external-id m42 -stop local_api -- ./sample */
#include <stdio.h>
#include <stdlib.h>

#include "gs_sdk.h"

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
