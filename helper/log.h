#ifndef HELPER_LOG_H
#define HELPER_LOG_H

#include <stdarg.h>
#include <stdio.h>

// One logging channel: structured events on stdout. Period.
//
// Every emit() call produces exactly one line:
//     @evt event=NAME level=LVL msg="human text" k=v k=v ...
//
// The Go CLI parses the @evt stream for the TUI. SSH users reading
// directly see the same line  the `msg` attribute is human-readable.
//
// stderr is reserved for catastrophic helper-level failures (crashes,
// panic-style aborts). Regular progress, warnings, errors all flow
// through emit() as events.
//
// Level filter: LOG_DEBUG is suppressed entirely unless log_init was
// called with verbose=1. No duplicate output anywhere.

typedef enum {
    LOG_DEBUG = 0,
    LOG_INFO  = 1,
    LOG_WARN  = 2,
    LOG_ERROR = 3,
} log_level_t;

void log_init(int verbose);

// Redirect the event stream to a different FILE* (default: stdout). Used
// when streaming the IPA on stdout: events move to stderr so they don't
// collide with the data channel.
void log_set_stream(FILE *f);

// Attribute buffer. Stack-allocate, reuse per emit().
typedef struct {
    char buf[2048];
    int  len;
    int  overflow;
} attrs_t;

void attrs_init(attrs_t *a);
void attrs_str (attrs_t *a, const char *key, const char *val);  // auto-quoted on whitespace
void attrs_int (attrs_t *a, const char *key, long long val);
void attrs_uint(attrs_t *a, const char *key, unsigned long long val);
void attrs_hex (attrs_t *a, const char *key, unsigned long long val);  // 0xNNN
void attrs_fmt (attrs_t *a, const char *key, const char *fmt, ...)
    __attribute__((format(printf, 3, 4)));

// One event = one line on stdout. event_name is dot-namespaced
// ("image.done"). attrs may be NULL. human_fmt may be NULL (then the
// event carries only structured attrs; SSH users see only k=v).
//
// LOG_DEBUG events are dropped when verbose=0.
void emit(log_level_t level, const char *event_name, const attrs_t *a,
          const char *human_fmt, ...)
    __attribute__((format(printf, 4, 5)));

// Free-form level-tagged messages with no stable event name. Wraps emit()
// with event="log". Useful for chatter that doesn't deserve its own name.
void dbg(const char *fmt, ...) __attribute__((format(printf, 1, 2)));
void inf(const char *fmt, ...) __attribute__((format(printf, 1, 2)));
void wrn(const char *fmt, ...) __attribute__((format(printf, 1, 2)));
void er (const char *fmt, ...) __attribute__((format(printf, 1, 2)));

// Format helper that returns a static thread-local buffer.
const char *human_bytes(unsigned long long n);

// True if the requested level would actually be emitted (cheap gate
// before expensive printf args).
int log_level_visible(log_level_t level);

#endif // HELPER_LOG_H
