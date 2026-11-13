/*
 * gs_sdk: see gs_sdk.h. Licensed under the Apache License, Version 2.0.
 *
 * One HTTP/1.1 request per call over its own connection (Connection: close), so a client needs no locking around
 * the network and a long-poll never holds up another thread. The JSON reader is just big enough for the replies.
 */
#define GS_SDK_BUILD 1
#if defined(_MSC_VER) && !defined(_CRT_SECURE_NO_WARNINGS)
#  define _CRT_SECURE_NO_WARNINGS /* getenv and sscanf are used safely here */
#endif
#include "gs_sdk.h"

#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#  ifndef WIN32_LEAN_AND_MEAN
#    define WIN32_LEAN_AND_MEAN
#  endif
#  include <winsock2.h>
#  include <ws2tcpip.h>
#  include <windows.h>
typedef SOCKET gs_socket;
#  define GS_BAD_SOCKET INVALID_SOCKET
#  define gs_closesocket closesocket
typedef CRITICAL_SECTION gs_mutex;
#  define gs_mutex_init(m) InitializeCriticalSection(m)
#  define gs_mutex_lock(m) EnterCriticalSection(m)
#  define gs_mutex_unlock(m) LeaveCriticalSection(m)
#  define gs_mutex_free(m) DeleteCriticalSection(m)
#  ifdef _MSC_VER
#    pragma comment(lib, "ws2_32.lib")
#  endif
#else
#  include <netdb.h>
#  include <netinet/in.h>
#  include <netinet/tcp.h>
#  include <pthread.h>
#  include <sys/socket.h>
#  include <sys/time.h>
#  include <sys/types.h>
#  include <unistd.h>
typedef int gs_socket;
#  define GS_BAD_SOCKET (-1)
#  define gs_closesocket close
typedef pthread_mutex_t gs_mutex;
#  define gs_mutex_init(m) pthread_mutex_init(m, NULL)
#  define gs_mutex_lock(m) pthread_mutex_lock(m)
#  define gs_mutex_unlock(m) pthread_mutex_unlock(m)
#  define gs_mutex_free(m) pthread_mutex_destroy(m)
#endif

#define GS_MAX_REPLY (1 << 20)
#define GS_ERR_SIZE 256

struct gs_client {
	char host[256];
	char port[8];
	char base[256]; /* path before /v0, usually empty */
	char *token;
	gs_mutex mu; /* guards err and the player list */
	char err[GS_ERR_SIZE];
	char **players;
	size_t nplayers, capplayers;
};

/* --- small helpers ------------------------------------------------------------------------------------------- */

static gs_result fail(gs_client *c, gs_result r, const char *fmt, const char *arg) {
	if (c) {
		gs_mutex_lock(&c->mu);
		snprintf(c->err, sizeof c->err, fmt, arg ? arg : "");
		gs_mutex_unlock(&c->mu);
	}
	return r;
}

static char *dupstr(const char *s) {
	size_t n = strlen(s) + 1;
	char *d = (char *)malloc(n);
	if (d) memcpy(d, s, n);
	return d;
}

/* A growing byte buffer. */
typedef struct {
	char *p;
	size_t n, cap;
	int bad;
} buf;

static void put(buf *b, const char *s, size_t n) {
	if (b->bad) return;
	if (b->n + n + 1 > b->cap) {
		size_t cap = b->cap ? b->cap * 2 : 256;
		while (cap < b->n + n + 1) cap *= 2;
		char *p = (char *)realloc(b->p, cap);
		if (!p) {
			b->bad = 1;
			return;
		}
		b->p = p;
		b->cap = cap;
	}
	memcpy(b->p + b->n, s, n);
	b->n += n;
	b->p[b->n] = 0;
}

static void puts_(buf *b, const char *s) { put(b, s, strlen(s)); }

/* putjstr writes s as a JSON string. */
static void putjstr(buf *b, const char *s) {
	puts_(b, "\"");
	for (; *s; s++) {
		unsigned char ch = (unsigned char)*s;
		char e[8];
		switch (ch) {
		case '"': puts_(b, "\\\""); break;
		case '\\': puts_(b, "\\\\"); break;
		case '\n': puts_(b, "\\n"); break;
		case '\r': puts_(b, "\\r"); break;
		case '\t': puts_(b, "\\t"); break;
		default:
			if (ch < 0x20) {
				snprintf(e, sizeof e, "\\u%04x", ch);
				puts_(b, e);
			} else {
				put(b, (const char *)&ch, 1);
			}
		}
	}
	puts_(b, "\"");
}

/* --- JSON, as much as the replies need ------------------------------------------------------------------------- */

typedef enum { J_NULL, J_BOOL, J_NUM, J_STR, J_ARR, J_OBJ } jtype;

typedef struct jval {
	jtype t;
	double num;
	char *str;          /* J_STR */
	struct jval **kids; /* J_ARR, J_OBJ */
	char **keys;        /* J_OBJ */
	size_t n;
} jval;

static void jfree(jval *v) {
	size_t i;
	if (!v) return;
	for (i = 0; i < v->n; i++) {
		jfree(v->kids[i]);
		if (v->keys) free(v->keys[i]);
	}
	free(v->kids);
	free(v->keys);
	free(v->str);
	free(v);
}

typedef struct {
	const char *s, *end;
	int depth;
} jp;

static void ws(jp *p) {
	while (p->s < p->end && isspace((unsigned char)*p->s)) p->s++;
}

static jval *jnew(jtype t) {
	jval *v = (jval *)calloc(1, sizeof(jval));
	if (v) v->t = t;
	return v;
}

static void utf8(buf *b, unsigned cp) {
	char o[4];
	if (cp < 0x80) {
		o[0] = (char)cp;
		put(b, o, 1);
	} else if (cp < 0x800) {
		o[0] = (char)(0xC0 | (cp >> 6));
		o[1] = (char)(0x80 | (cp & 0x3F));
		put(b, o, 2);
	} else if (cp < 0x10000) {
		o[0] = (char)(0xE0 | (cp >> 12));
		o[1] = (char)(0x80 | ((cp >> 6) & 0x3F));
		o[2] = (char)(0x80 | (cp & 0x3F));
		put(b, o, 3);
	} else {
		o[0] = (char)(0xF0 | (cp >> 18));
		o[1] = (char)(0x80 | ((cp >> 12) & 0x3F));
		o[2] = (char)(0x80 | ((cp >> 6) & 0x3F));
		o[3] = (char)(0x80 | (cp & 0x3F));
		put(b, o, 4);
	}
}

static int hex4(const char *s, unsigned *out) {
	unsigned v = 0;
	int i;
	for (i = 0; i < 4; i++) {
		char ch = s[i];
		v <<= 4;
		if (ch >= '0' && ch <= '9') v |= (unsigned)(ch - '0');
		else if (ch >= 'a' && ch <= 'f') v |= (unsigned)(ch - 'a' + 10);
		else if (ch >= 'A' && ch <= 'F') v |= (unsigned)(ch - 'A' + 10);
		else return 0;
	}
	*out = v;
	return 1;
}

static char *jstring(jp *p) {
	buf b = {0};
	if (p->s >= p->end || *p->s != '"') return NULL;
	p->s++;
	puts_(&b, "");
	while (p->s < p->end && *p->s != '"') {
		char ch = *p->s++;
		if (ch != '\\') {
			put(&b, &ch, 1);
			continue;
		}
		if (p->s >= p->end) break;
		ch = *p->s++;
		switch (ch) {
		case 'n': put(&b, "\n", 1); break;
		case 't': put(&b, "\t", 1); break;
		case 'r': put(&b, "\r", 1); break;
		case 'b': put(&b, "\b", 1); break;
		case 'f': put(&b, "\f", 1); break;
		case 'u': {
			unsigned cp, lo;
			if (p->end - p->s < 4 || !hex4(p->s, &cp)) goto bad;
			p->s += 4;
			if (cp >= 0xD800 && cp < 0xDC00 && p->end - p->s >= 6 && p->s[0] == '\\' && p->s[1] == 'u' && hex4(p->s + 2, &lo) &&
			    lo >= 0xDC00 && lo < 0xE000) {
				cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
				p->s += 6;
			}
			utf8(&b, cp);
			break;
		}
		default: put(&b, &ch, 1);
		}
	}
	if (p->s >= p->end || b.bad) goto bad;
	p->s++;
	return b.p;
bad:
	free(b.p);
	return NULL;
}

static jval *jparse(jp *p);

static int jadd(jval *v, char *key, jval *kid) {
	jval **k = (jval **)realloc(v->kids, (v->n + 1) * sizeof *k);
	if (!k) return 0;
	v->kids = k;
	if (v->t == J_OBJ) {
		char **ks = (char **)realloc(v->keys, (v->n + 1) * sizeof *ks);
		if (!ks) return 0;
		v->keys = ks;
		v->keys[v->n] = key;
	}
	v->kids[v->n++] = kid;
	return 1;
}

static jval *jparse(jp *p) {
	jval *v = NULL;
	ws(p);
	if (p->s >= p->end || ++p->depth > 32) return NULL;
	switch (*p->s) {
	case '{':
		v = jnew(J_OBJ);
		p->s++;
		ws(p);
		if (p->s < p->end && *p->s == '}') {
			p->s++;
			break;
		}
		for (;;) {
			char *k;
			jval *kid;
			ws(p);
			k = jstring(p);
			if (!k) goto bad;
			ws(p);
			if (p->s >= p->end || *p->s != ':') {
				free(k);
				goto bad;
			}
			p->s++;
			kid = jparse(p);
			if (!kid || !v || !jadd(v, k, kid)) {
				free(k);
				jfree(kid);
				goto bad;
			}
			ws(p);
			if (p->s < p->end && *p->s == ',') {
				p->s++;
				continue;
			}
			if (p->s < p->end && *p->s == '}') {
				p->s++;
				break;
			}
			goto bad;
		}
		break;
	case '[':
		v = jnew(J_ARR);
		p->s++;
		ws(p);
		if (p->s < p->end && *p->s == ']') {
			p->s++;
			break;
		}
		for (;;) {
			jval *kid = jparse(p);
			if (!kid || !v || !jadd(v, NULL, kid)) {
				jfree(kid);
				goto bad;
			}
			ws(p);
			if (p->s < p->end && *p->s == ',') {
				p->s++;
				continue;
			}
			if (p->s < p->end && *p->s == ']') {
				p->s++;
				break;
			}
			goto bad;
		}
		break;
	case '"':
		v = jnew(J_STR);
		if (v && !(v->str = jstring(p))) goto bad;
		break;
	case 't':
	case 'f':
	case 'n': {
		const char *w = *p->s == 't' ? "true" : *p->s == 'f' ? "false" : "null";
		size_t n = strlen(w);
		if ((size_t)(p->end - p->s) < n || strncmp(p->s, w, n) != 0) return NULL;
		v = jnew(*w == 'n' ? J_NULL : J_BOOL);
		if (v) v->num = *w == 't';
		p->s += n;
		break;
	}
	default: {
		char tmp[64];
		size_t n = 0;
		while (p->s + n < p->end && n < sizeof tmp - 1 && strchr("+-0123456789.eE", p->s[n])) n++;
		if (n == 0) return NULL;
		memcpy(tmp, p->s, n);
		tmp[n] = 0;
		v = jnew(J_NUM);
		if (v) v->num = strtod(tmp, NULL);
		p->s += n;
	}
	}
	p->depth--;
	return v;
bad:
	jfree(v);
	return NULL;
}

static jval *jget(const jval *v, const char *key) {
	size_t i;
	if (!v || v->t != J_OBJ) return NULL;
	for (i = 0; i < v->n; i++)
		if (strcmp(v->keys[i], key) == 0) return v->kids[i];
	return NULL;
}

static const char *jstr(const jval *v, const char *key) {
	jval *k = jget(v, key);
	return k && k->t == J_STR ? k->str : NULL;
}

/* --- HTTP ------------------------------------------------------------------------------------------------------ */

#ifdef _WIN32
static INIT_ONCE wsa_once = INIT_ONCE_STATIC_INIT;
static BOOL CALLBACK wsa_start(PINIT_ONCE o, PVOID p, PVOID *ctx) {
	WSADATA d;
	(void)o;
	(void)p;
	(void)ctx;
	return WSAStartup(MAKEWORD(2, 2), &d) == 0;
}
#endif

typedef struct {
	int status;
	char *body; /* NUL-terminated */
	size_t len;
} reply;

/* dechunk turns a chunked body into a plain one, in place; 0 if it is not well formed. */
static int dechunk(char *b, size_t *len) {
	char *r = b, *w = b, *end = b + *len;
	for (;;) {
		char *nl;
		unsigned long n;
		nl = (char *)memchr(r, '\n', (size_t)(end - r));
		if (!nl) return 0;
		n = strtoul(r, NULL, 16);
		r = nl + 1;
		if (n == 0) break;
		if ((size_t)(end - r) < n) return 0;
		memmove(w, r, n);
		w += n;
		r += n;
		if (r < end && *r == '\r') r++;
		if (r < end && *r == '\n') r++;
	}
	*w = 0;
	*len = (size_t)(w - b);
	return 1;
}

/* call makes one request and reads the whole reply. timeout_ms bounds waiting for it. */
static gs_result call(gs_client *c, const char *method, const char *path, const char *body, int timeout_ms, reply *out) {
	struct addrinfo hints, *res = NULL, *ai;
	gs_socket s = GS_BAD_SOCKET;
	buf req = {0}, in = {0};
	char line[512], *hdr_end, *p;
	size_t blen = body ? strlen(body) : 0, sent = 0;
	gs_result r = GS_OK;
	memset(out, 0, sizeof *out);
#ifdef _WIN32
	InitOnceExecuteOnce(&wsa_once, wsa_start, NULL, NULL);
#endif
	memset(&hints, 0, sizeof hints);
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(c->host, c->port, &hints, &res) != 0) return fail(c, GS_ERR_CONNECT, "cannot resolve the agent's address %s", c->host);
	for (ai = res; ai; ai = ai->ai_next) {
		s = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (s == GS_BAD_SOCKET) continue;
		if (connect(s, ai->ai_addr, (int)ai->ai_addrlen) == 0) break;
		gs_closesocket(s);
		s = GS_BAD_SOCKET;
	}
	freeaddrinfo(res);
	if (s == GS_BAD_SOCKET) return fail(c, GS_ERR_CONNECT, "the agent at %s does not answer", c->host);
	{
#ifdef _WIN32
		DWORD t = (DWORD)timeout_ms;
#else
		struct timeval t;
		t.tv_sec = timeout_ms / 1000;
		t.tv_usec = (timeout_ms % 1000) * 1000;
#endif
		setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (const char *)&t, sizeof t);
		setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, (const char *)&t, sizeof t);
	}
	puts_(&req, method);
	puts_(&req, " ");
	puts_(&req, c->base);
	puts_(&req, path);
	puts_(&req, " HTTP/1.1\r\nHost: ");
	puts_(&req, c->host);
	puts_(&req, ":");
	puts_(&req, c->port);
	puts_(&req, "\r\nConnection: close\r\nAccept: application/json\r\n");
	puts_(&req, "Authorization: Bearer ");
	puts_(&req, c->token);
	puts_(&req, "\r\n");
	if (body) {
		snprintf(line, sizeof line, "Content-Type: application/json\r\nContent-Length: %lu\r\n", (unsigned long)blen);
		puts_(&req, line);
	} else if (strcmp(method, "GET") != 0) {
		puts_(&req, "Content-Length: 0\r\n");
	}
	puts_(&req, "\r\n");
	if (body) puts_(&req, body);
	if (req.bad) {
		r = GS_ERR_MEMORY;
		goto done;
	}
	while (sent < req.n) {
		int n = (int)send(s, req.p + sent, (int)(req.n - sent), 0);
		if (n <= 0) {
			r = fail(c, GS_ERR_CONNECT, "sending to the agent failed%s", "");
			goto done;
		}
		sent += (size_t)n;
	}
	for (;;) {
		char chunk[4096];
		int n = (int)recv(s, chunk, sizeof chunk, 0);
		if (n == 0) break;
		if (n < 0) {
			r = fail(c, GS_ERR_CONNECT, "the agent's reply did not come in time%s", "");
			goto done;
		}
		put(&in, chunk, (size_t)n);
		if (in.bad || in.n > GS_MAX_REPLY) {
			r = fail(c, GS_ERR_REPLY, "the agent's reply is too large%s", "");
			goto done;
		}
	}
	if (!in.p || strncmp(in.p, "HTTP/1.", 7) != 0 || in.n < 12) {
		r = fail(c, GS_ERR_REPLY, "not an HTTP reply%s", "");
		goto done;
	}
	out->status = atoi(in.p + 9);
	hdr_end = strstr(in.p, "\r\n\r\n");
	if (!hdr_end) {
		r = fail(c, GS_ERR_REPLY, "a reply without its headers' end%s", "");
		goto done;
	}
	*hdr_end = 0;
	p = hdr_end + 4;
	out->len = in.n - (size_t)(p - in.p);
	out->body = (char *)malloc(out->len + 1);
	if (!out->body) {
		r = GS_ERR_MEMORY;
		goto done;
	}
	memcpy(out->body, p, out->len);
	out->body[out->len] = 0;
	{
		/* Chunked replies (the agent's longer ones): headers are case-insensitive. */
		char *h;
		for (h = in.p; *h; h++) *h = (char)tolower((unsigned char)*h);
		if (strstr(in.p, "transfer-encoding: chunked") && !dechunk(out->body, &out->len)) {
			r = fail(c, GS_ERR_REPLY, "a broken chunked reply%s", "");
			goto done;
		}
	}
done:
	gs_closesocket(s);
	free(req.p);
	free(in.p);
	if (r != GS_OK) {
		free(out->body);
		out->body = NULL;
	}
	return r;
}

/* expect turns a reply into a result: the wanted statuses are fine, a refusal carries the agent's own reason. */
static gs_result expect(gs_client *c, gs_result r, reply *rep, int ok1, int ok2) {
	if (r != GS_OK) return r;
	if (rep->status == ok1 || rep->status == ok2) return GS_OK;
	{
		jp p = {rep->body, rep->body + rep->len, 0};
		jval *v = jparse(&p);
		const char *d = jstr(v, "detail");
		char msg[200];
		snprintf(msg, sizeof msg, "%d %.180s", rep->status, d ? d : "");
		fail(c, GS_ERR_REFUSED, "the agent refused: %s", msg);
		jfree(v);
	}
	return GS_ERR_REFUSED;
}

static gs_result simple(gs_client *c, const char *method, const char *path, const char *body, int ok1, int ok2) {
	reply rep;
	gs_result r;
	if (!c) return GS_ERR_ARGUMENT;
	r = expect(c, call(c, method, path, body, 10000, &rep), &rep, ok1, ok2);
	free(rep.body);
	return r;
}

/* --- the calls ------------------------------------------------------------------------------------------------- */

int gs_sdk_version(void) { return GS_SDK_VERSION; }

gs_result gs_open(gs_client **out) {
	/* GS_AGENT_URL is the older name of GS_LOCAL_API_URL: read when the new one is not set. */
	const char *url = getenv("GS_LOCAL_API_URL"), *tok = getenv("GS_TOKEN");
	if (!url || !*url) url = getenv("GS_AGENT_URL");
	if (!out) return GS_ERR_ARGUMENT;
	*out = NULL;
	if (!url || !*url || !tok || !*tok) return GS_ERR_NO_AGENT;
	return gs_open_with(url, tok, out);
}

gs_result gs_open_with(const char *agent_url, const char *token, gs_client **out) {
	gs_client *c;
	const char *h, *slash, *colon;
	size_t hl;
	if (!agent_url || !token || !out) return GS_ERR_ARGUMENT;
	*out = NULL;
	if (strncmp(agent_url, "http://", 7) != 0) return GS_ERR_ARGUMENT;
	h = agent_url + 7;
	slash = strchr(h, '/');
	if (!slash) slash = h + strlen(h);
	colon = h;
	if (*h == '[') { /* [IPv6]:port */
		colon = (const char *)memchr(h, ']', (size_t)(slash - h));
		if (!colon) return GS_ERR_ARGUMENT;
		colon = (const char *)memchr(colon, ':', (size_t)(slash - colon));
	} else {
		colon = (const char *)memchr(h, ':', (size_t)(slash - h));
	}
	c = (gs_client *)calloc(1, sizeof *c);
	if (!c) return GS_ERR_MEMORY;
	hl = (size_t)((colon ? colon : slash) - h);
	if (*h == '[') {
		h++;
		hl -= 2;
	}
	if (hl == 0 || hl >= sizeof c->host || strlen(slash) >= sizeof c->base) {
		free(c);
		return GS_ERR_ARGUMENT;
	}
	memcpy(c->host, h, hl);
	if (colon) {
		size_t pl = (size_t)(slash - colon - 1);
		if (pl == 0 || pl >= sizeof c->port) {
			free(c);
			return GS_ERR_ARGUMENT;
		}
		memcpy(c->port, colon + 1, pl);
	} else {
		strcpy(c->port, "80");
	}
	strcpy(c->base, slash);
	if (*c->base && c->base[strlen(c->base) - 1] == '/') c->base[strlen(c->base) - 1] = 0;
	c->token = dupstr(token);
	if (!c->token) {
		free(c);
		return GS_ERR_MEMORY;
	}
	gs_mutex_init(&c->mu);
	*out = c;
	return GS_OK;
}

void gs_close(gs_client *c) {
	size_t i;
	if (!c) return;
	for (i = 0; i < c->nplayers; i++) free(c->players[i]);
	free(c->players);
	free(c->token);
	gs_mutex_free(&c->mu);
	free(c);
}

const char *gs_last_error(const gs_client *c) { return c ? c->err : "no client"; }

gs_result gs_ready(gs_client *c) { return simple(c, "POST", "/v0/self/ready", NULL, 204, 200); }

gs_result gs_health(gs_client *c) { return simple(c, "POST", "/v0/self/health", NULL, 204, 200); }

gs_result gs_stop(gs_client *c, const char *reason) {
	buf b = {0};
	gs_result r;
	puts_(&b, "{\"reason\":");
	putjstr(&b, reason ? reason : "");
	puts_(&b, "}");
	if (b.bad) return GS_ERR_MEMORY;
	r = simple(c, "POST", "/v0/self/stop", b.p, 202, 200);
	free(b.p);
	return r;
}

gs_result gs_saved(gs_client *c, const char *keep) {
	buf b = {0};
	reply rep;
	gs_result r;
	if (!c) return GS_ERR_ARGUMENT;
	if (keep && *keep) {
		puts_(&b, "{\"keep\":");
		putjstr(&b, keep);
		puts_(&b, "}");
		if (b.bad) return GS_ERR_MEMORY;
	}
	r = call(c, "POST", "/v0/self/saved", b.p ? b.p : "{}", 10000, &rep);
	if (r == GS_OK && rep.status == 404) r = fail(c, GS_ERR_UNSUPPORTED, "this agent has no %s call", "saved");
	else r = expect(c, r, &rep, 202, 200);
	free(rep.body);
	free(b.p);
	return r;
}

struct gs_allocation {
	jval *v;
	jval *env;
	jval *labels;
};

static gs_result wait_for(gs_client *c, const char *what, int wait_ms, jval **out) {
	char path[96];
	reply rep;
	gs_result r;
	if (!c || !out) return GS_ERR_ARGUMENT;
	*out = NULL;
	if (wait_ms < 0) wait_ms = 0;
	if (wait_ms > 60000) wait_ms = 60000; /* the agent holds a call open for 60 s at most */
	snprintf(path, sizeof path, "/v0/self/%s?wait=%dms", what, wait_ms);
	r = call(c, "GET", path, NULL, wait_ms + 10000, &rep);
	if (r == GS_OK && rep.status == 204) {
		free(rep.body);
		return GS_ERR_NONE_YET;
	}
	r = expect(c, r, &rep, 200, 200);
	if (r == GS_OK) {
		jp p = {rep.body, rep.body + rep.len, 0};
		*out = jparse(&p);
		if (!*out || (*out)->t != J_OBJ) {
			jfree(*out);
			*out = NULL;
			r = fail(c, GS_ERR_REPLY, "the agent's %s could not be read", what);
		}
	}
	free(rep.body);
	return r;
}

gs_result gs_wait_allocation(gs_client *c, int wait_ms, gs_allocation **out) {
	jval *v;
	gs_result r;
	if (!out) return GS_ERR_ARGUMENT;
	*out = NULL;
	r = wait_for(c, "allocation", wait_ms, &v);
	if (r != GS_OK) return r;
	*out = (gs_allocation *)calloc(1, sizeof **out);
	if (!*out) {
		jfree(v);
		return GS_ERR_MEMORY;
	}
	(*out)->v = v;
	(*out)->env = jget(v, "env");
	(*out)->labels = jget(v, "labels");
	return GS_OK;
}

const char *gs_allocation_game_server_id(const gs_allocation *a) {
	const char *s = a ? jstr(a->v, "game_server_id") : NULL;
	return s ? s : "";
}

const char *gs_allocation_external_id(const gs_allocation *a) {
	const char *s = a ? jstr(a->v, "external_id") : NULL;
	return s ? s : "";
}

const char *gs_allocation_arg(const gs_allocation *a, size_t i) {
	const jval *args = a ? jget(a->v, "args") : NULL;
	if (!args || args->t != J_ARR || i >= args->n) return NULL;
	return args->kids[i]->t == J_STR ? args->kids[i]->str : "";
}

const char *gs_allocation_env(const gs_allocation *a, const char *name) { return a && name ? jstr(a->env, name) : NULL; }

const char *gs_allocation_label(const gs_allocation *a, const char *name) { return a && name ? jstr(a->labels, name) : NULL; }

static const char *nth_name(const jval *o, size_t i) { return o && o->t == J_OBJ && i < o->n ? o->keys[i] : NULL; }

static const char *nth_value(const jval *o, size_t i) {
	if (!o || o->t != J_OBJ || i >= o->n) return NULL;
	return o->kids[i]->t == J_STR ? o->kids[i]->str : "";
}

const char *gs_allocation_env_name(const gs_allocation *a, size_t i) { return a ? nth_name(a->env, i) : NULL; }
const char *gs_allocation_env_value(const gs_allocation *a, size_t i) { return a ? nth_value(a->env, i) : NULL; }
const char *gs_allocation_label_name(const gs_allocation *a, size_t i) { return a ? nth_name(a->labels, i) : NULL; }
const char *gs_allocation_label_value(const gs_allocation *a, size_t i) { return a ? nth_value(a->labels, i) : NULL; }

void gs_allocation_free(gs_allocation *a) {
	if (!a) return;
	jfree(a->v);
	free(a);
}

/* days_from_civil: days since 1970-01-01 of a Gregorian date (Howard Hinnant's algorithm). */
static int64_t days_from_civil(int64_t y, unsigned m, unsigned d) {
	int64_t era;
	unsigned yoe, doy, doe;
	y -= m <= 2;
	era = (y >= 0 ? y : y - 399) / 400;
	yoe = (unsigned)(y - era * 400);
	doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
	doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
	return era * 146097 + (int64_t)doe - 719468;
}

/* rfc3339_ms reads "2026-10-19T12:34:56.789Z" (or with an offset) as Unix milliseconds; 0 if it cannot. */
static int64_t rfc3339_ms(const char *s) {
	int y, mo, d, h, mi, sec, n = 0;
	int64_t ms = 0, t;
	if (!s || sscanf(s, "%d-%d-%dT%d:%d:%d%n", &y, &mo, &d, &h, &mi, &sec, &n) != 6) return 0;
	s += n;
	if (*s == '.') {
		int digits = 0;
		s++;
		while (isdigit((unsigned char)*s)) {
			if (digits < 3) ms = ms * 10 + (*s - '0');
			digits++;
			s++;
		}
		while (digits < 3) {
			ms *= 10;
			digits++;
		}
	}
	t = ((days_from_civil(y, (unsigned)mo, (unsigned)d) * 24 + h) * 60 + mi) * 60 + sec;
	if (*s == '+' || *s == '-') {
		int oh = 0, om = 0;
		sscanf(s + 1, "%d:%d", &oh, &om);
		t -= (*s == '+' ? 1 : -1) * (oh * 3600 + om * 60);
	}
	return t * 1000 + ms;
}

gs_result gs_wait_shutdown(gs_client *c, int wait_ms, int64_t *deadline_unix_ms, char *reason, size_t reason_size) {
	jval *v;
	gs_result r = wait_for(c, "shutdown", wait_ms, &v);
	if (r != GS_OK) return r;
	if (deadline_unix_ms) *deadline_unix_ms = rfc3339_ms(jstr(v, "deadline"));
	if (reason && reason_size) {
		const char *s = jstr(v, "reason");
		snprintf(reason, reason_size, "%s", s ? s : "");
	}
	jfree(v);
	return GS_OK;
}

/* send_players sends the whole list (taken under the lock, sent outside it). */
static gs_result send_players(gs_client *c) {
	buf b = {0};
	size_t i;
	gs_result r;
	gs_mutex_lock(&c->mu);
	puts_(&b, "{\"ids\":[");
	for (i = 0; i < c->nplayers; i++) {
		if (i) puts_(&b, ",");
		putjstr(&b, c->players[i]);
	}
	puts_(&b, "]}");
	gs_mutex_unlock(&c->mu);
	if (b.bad) return GS_ERR_MEMORY;
	r = simple(c, "PUT", "/v0/self/players", b.p, 200, 204);
	free(b.p);
	return r;
}

/* id_problem says what is wrong with a player ID, or NULL: the agent's rule, 1 to 128 characters of UTF-8 text with no
 * control characters (U+0000-U+001F, U+007F-U+009F). The agent refuses a list holding such an ID whole, so it is refused
 * here, before it joins the list, and the other players keep counting. */
static const char *id_problem(const char *s) {
	const unsigned char *p = (const unsigned char *)s;
	size_t n = 0;
	int i, len;
	unsigned long cp;
	if (!*p) return "is empty";
	while (*p) {
		if (p[0] < 0x80) cp = p[0], len = 1;
		else if ((p[0] & 0xE0) == 0xC0) cp = p[0] & 0x1F, len = 2;
		else if ((p[0] & 0xF0) == 0xE0) cp = p[0] & 0x0F, len = 3;
		else if ((p[0] & 0xF8) == 0xF0) cp = p[0] & 0x07, len = 4;
		else return "is not UTF-8 text";
		for (i = 1; i < len; i++) {
			if ((p[i] & 0xC0) != 0x80) return "is not UTF-8 text"; /* also the end of the string */
			cp = cp << 6 | (p[i] & 0x3F);
		}
		if ((len == 2 && cp < 0x80) || (len == 3 && cp < 0x800) || (len == 4 && (cp < 0x10000 || cp > 0x10FFFF)) ||
		    (cp >= 0xD800 && cp <= 0xDFFF))
			return "is not UTF-8 text";
		if (cp < 0x20 || (cp >= 0x7F && cp <= 0x9F)) return "has a control character";
		p += len;
		n++;
	}
	return n > 128 ? "is longer than 128 characters" : NULL;
}

gs_result gs_player_connect(gs_client *c, const char *player_id) {
	size_t i;
	const char *bad;
	if (!c || !player_id) return GS_ERR_ARGUMENT;
	if ((bad = id_problem(player_id)) != NULL)
		return fail(c, GS_ERR_ARGUMENT, "the player ID %s (1 to 128 characters, no control characters)", bad);
	gs_mutex_lock(&c->mu);
	for (i = 0; i < c->nplayers; i++) {
		if (strcmp(c->players[i], player_id) == 0) {
			gs_mutex_unlock(&c->mu);
			return GS_OK;
		}
	}
	if (c->nplayers == c->capplayers) {
		size_t cap = c->capplayers ? c->capplayers * 2 : 16;
		char **p = (char **)realloc(c->players, cap * sizeof *p);
		if (!p) {
			gs_mutex_unlock(&c->mu);
			return GS_ERR_MEMORY;
		}
		c->players = p;
		c->capplayers = cap;
	}
	c->players[c->nplayers] = dupstr(player_id);
	if (!c->players[c->nplayers]) {
		gs_mutex_unlock(&c->mu);
		return GS_ERR_MEMORY;
	}
	c->nplayers++;
	gs_mutex_unlock(&c->mu);
	return send_players(c);
}

gs_result gs_player_disconnect(gs_client *c, const char *player_id) {
	size_t i;
	if (!c || !player_id) return GS_ERR_ARGUMENT;
	gs_mutex_lock(&c->mu);
	for (i = 0; i < c->nplayers; i++) {
		if (strcmp(c->players[i], player_id) == 0) {
			free(c->players[i]);
			memmove(c->players + i, c->players + i + 1, (c->nplayers - i - 1) * sizeof *c->players);
			c->nplayers--;
			gs_mutex_unlock(&c->mu);
			return send_players(c);
		}
	}
	gs_mutex_unlock(&c->mu);
	return GS_OK;
}

gs_result gs_set_player_count(gs_client *c, int count) {
	char body[64];
	if (!c || count < 0) return GS_ERR_ARGUMENT;
	snprintf(body, sizeof body, "{\"count\":%d}", count);
	return simple(c, "PUT", "/v0/self/players", body, 200, 204);
}

gs_result gs_set_label(gs_client *c, const char *name, const char *value) {
	buf b = {0};
	gs_result r;
	if (!c || !name || !*name) return GS_ERR_ARGUMENT;
	puts_(&b, "{");
	putjstr(&b, name);
	puts_(&b, ":");
	if (value) putjstr(&b, value);
	else puts_(&b, "null");
	puts_(&b, "}");
	if (b.bad) return GS_ERR_MEMORY;
	r = simple(c, "PATCH", "/v0/self/labels", b.p, 200, 204);
	free(b.p);
	return r;
}

/* ms writes a number of milliseconds with three decimals and a point, whatever the game's locale. */
static void ms(char *out, size_t n, double v) {
	long long us = (long long)(v * 1000 + 0.5);
	snprintf(out, n, "%lld.%03lld", us / 1000, us % 1000);
}

gs_result gs_tick(gs_client *c, double p50_ms, double p99_ms, int hz) {
	char body[160], a[40], b[40];
	if (!c || p99_ms < 0 || p50_ms < 0 || p99_ms > 1e9) return GS_ERR_ARGUMENT;
	ms(a, sizeof a, p50_ms);
	ms(b, sizeof b, p99_ms);
	if (hz > 0) snprintf(body, sizeof body, "{\"p50_ms\":%s,\"p99_ms\":%s,\"hz\":%d}", a, b, hz);
	else snprintf(body, sizeof body, "{\"p50_ms\":%s,\"p99_ms\":%s}", a, b);
	return simple(c, "POST", "/v0/self/tick", body, 204, 200);
}
