/*
 * gs_sdk: a small C library for the local API (the GS_* variables, where GS stands for game server, and the HTTP
 * calls the machine serves on GS_LOCAL_API_URL). It is optional: everything it does is a plain HTTP call a game server can
 * make itself. Standard C and the operating system's sockets only; vendor the two files.
 *
 * Licensed under the Apache License, Version 2.0.
 *
 * Every call returns GS_OK or a negative gs_result; gs_last_error() says why. A client may be used from several
 * threads; the waiting calls block only the thread that makes them.
 */
#ifndef GS_SDK_H
#define GS_SDK_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#if defined(_WIN32) && defined(GS_SDK_SHARED)
#  ifdef GS_SDK_BUILD
#    define GS_API __declspec(dllexport)
#  else
#    define GS_API __declspec(dllimport)
#  endif
#elif defined(__GNUC__) && defined(GS_SDK_SHARED)
#  define GS_API __attribute__((visibility("default")))
#else
#  define GS_API
#endif

/* The SDK version this header describes. A later version only adds functions. */
#define GS_SDK_VERSION 2

typedef enum gs_result {
	GS_OK = 0,
	GS_ERR_NO_AGENT = -1, /* GS_LOCAL_API_URL or GS_TOKEN is not set: not running on a game server machine */
	GS_ERR_CONNECT = -2,  /* the agent did not answer */
	GS_ERR_REFUSED = -3,  /* the agent refused the call (gs_last_error has its reason) */
	GS_ERR_NONE_YET = -4, /* nothing within the wait: no allocation, no shutdown notice */
	GS_ERR_ARGUMENT = -5,
	GS_ERR_MEMORY = -6,
	GS_ERR_REPLY = -7, /* the agent's reply could not be read */
	GS_ERR_UNSUPPORTED = -8 /* the agent is older than this call: carry on without it */
} gs_result;

typedef struct gs_client gs_client;
typedef struct gs_allocation gs_allocation;

/* gs_open makes a client from the GS_LOCAL_API_URL (or its older name GS_AGENT_URL) and GS_TOKEN variables;
 * gs_open_with from given values. */
GS_API gs_result gs_open(gs_client **out);
GS_API gs_result gs_open_with(const char *agent_url, const char *token, gs_client **out);
GS_API void gs_close(gs_client *c);
/* gs_last_error is the reason the last failed call on this client failed (valid until the next call). */
GS_API const char *gs_last_error(const gs_client *c);
GS_API int gs_sdk_version(void);

/* gs_ready: the game server takes players now (a ready server: it waits for its match). */
GS_API gs_result gs_ready(gs_client *c);
/* gs_health: still alive; call it more often than the version's health timeout, if it has one. */
GS_API gs_result gs_health(gs_client *c);
/* gs_stop: the game server ends itself (the match is over); a shutdown notice follows. */
GS_API gs_result gs_stop(gs_client *c, const char *reason);
/* gs_saved: a persistent server just finished a save, so it is backed up at once. keep (NULL for none, up to 100
 * characters) also keeps it under that name. Optional: the folder watcher backs saves up without it. GS_ERR_REFUSED with
 * gs_last_error's reason when it is too soon: at most once every 10 seconds, with keep once a minute. A game can keep 20
 * backups of a server; past that a save is an ordinary backup and the studio is told. */
GS_API gs_result gs_saved(gs_client *c, const char *keep);

/* gs_wait_allocation waits up to wait_ms (60000 at most) for the game server's match: GS_ERR_NONE_YET when there is
 * none yet, or early when a stop is requested meanwhile (check gs_wait_shutdown). A fresh start is allocated from the
 * start. Free the result with gs_allocation_free. */
GS_API gs_result gs_wait_allocation(gs_client *c, int wait_ms, gs_allocation **out);
/* The game server's own ID and your ID from the start request ("" when none was set). */
GS_API const char *gs_allocation_game_server_id(const gs_allocation *a);
GS_API const char *gs_allocation_external_id(const gs_allocation *a);
/* The start request's command-line arguments: the i-th, i from 0; NULL past the last. */
GS_API const char *gs_allocation_arg(const gs_allocation *a, size_t i);
/* The match's settings (secret ones included) and labels, by name; NULL when absent. */
GS_API const char *gs_allocation_env(const gs_allocation *a, const char *name);
GS_API const char *gs_allocation_label(const gs_allocation *a, const char *name);
/* Every setting, in order: name and value of the i-th, i from 0; NULL past the last. */
GS_API const char *gs_allocation_env_name(const gs_allocation *a, size_t i);
GS_API const char *gs_allocation_env_value(const gs_allocation *a, size_t i);
GS_API const char *gs_allocation_label_name(const gs_allocation *a, size_t i);
GS_API const char *gs_allocation_label_value(const gs_allocation *a, size_t i);
GS_API void gs_allocation_free(gs_allocation *a);

/* gs_wait_shutdown waits up to wait_ms (60000 at most) for a shutdown notice: then save and exit before deadline_unix_ms. Either output
 * may be NULL. GS_ERR_NONE_YET when there is none. */
GS_API gs_result gs_wait_shutdown(gs_client *c, int wait_ms, int64_t *deadline_unix_ms, char *reason, size_t reason_size);

/* Players: the client keeps the list and sends it whole on each change. The player count is also what pauses an empty
 * persistent server (when it is set to pause when empty). gs_set_player_count is for games that count without IDs.
 * A player ID is 1 to 128 characters of UTF-8 with no control characters: gs_player_connect refuses any other
 * (GS_ERR_ARGUMENT, often a name a player chose) and it never joins the list. */
GS_API gs_result gs_player_connect(gs_client *c, const char *player_id);
GS_API gs_result gs_player_disconnect(gs_client *c, const char *player_id);
GS_API gs_result gs_set_player_count(gs_client *c, int count);

/* gs_set_label sets a label on the game server (value NULL removes it): 1 to 63 of a-z 0-9 _ . - in its name. */
GS_API gs_result gs_set_label(gs_client *c, const char *name, const char *value);

/* gs_tick reports one second of update times (p50 and p99 in milliseconds) and the update rate, if known (0). */
GS_API gs_result gs_tick(gs_client *c, double p50_ms, double p99_ms, int hz);

#ifdef __cplusplus
}
#endif

#endif /* GS_SDK_H */
