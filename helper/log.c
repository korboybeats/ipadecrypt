#include "log.h"

#include <stdarg.h>
#include <stdio.h>
#include <string.h>

static int g_verbose = 0;

void log_init(int verbose) {
    g_verbose = verbose ? 1 : 0;
}

int log_level_visible(log_level_t level) {
    return !(level == LOG_DEBUG && !g_verbose);
}

static const char *level_name(log_level_t level) {
    switch (level) {
        case LOG_DEBUG: return "debug";
        case LOG_INFO:  return "info";
        case LOG_WARN:  return "warn";
        case LOG_ERROR: return "error";
    }
    return "info";
}

static int needs_quoting(const char *s) {
    if (!s || !*s) return 1;
    for (const char *p = s; *p; p++) {
        unsigned char c = (unsigned char)*p;
        if (c == ' ' || c == '\t' || c == '"' || c == '\\' || c == '=' || c < 0x20) return 1;
    }
    return 0;
}

static int append_quoted(char *out, int cap, const char *s) {
    int n = 0;
    if (n < cap) out[n++] = '"';
    for (const char *p = s; *p && n < cap; p++) {
        unsigned char c = (unsigned char)*p;
        if (c == '"' || c == '\\') {
            if (n + 2 > cap) break;
            out[n++] = '\\';
            out[n++] = (char)c;
        } else if (c < 0x20) {
            if (n + 4 > cap) break;
            n += snprintf(out + n, cap - n, "\\x%02x", c);
        } else {
            out[n++] = (char)c;
        }
    }
    if (n < cap) out[n++] = '"';
    return n;
}

static void attrs_append(attrs_t *a, const char *key, const char *val_raw,
                         int already_formatted) {
    if (a->overflow) return;
    int remaining = (int)sizeof(a->buf) - a->len;
    if (remaining < 8) { a->overflow = 1; return; }

    if (a->len > 0) {
        a->buf[a->len++] = ' ';
        remaining--;
    }

    int klen = (int)strlen(key);
    if (klen + 2 > remaining) { a->overflow = 1; return; }
    memcpy(a->buf + a->len, key, klen);
    a->len += klen;
    a->buf[a->len++] = '=';
    remaining -= klen + 1;

    if (already_formatted) {
        int vlen = (int)strlen(val_raw);
        if (vlen > remaining) { a->overflow = 1; return; }
        memcpy(a->buf + a->len, val_raw, vlen);
        a->len += vlen;
    } else if (needs_quoting(val_raw)) {
        int n = append_quoted(a->buf + a->len, remaining, val_raw);
        a->len += n;
    } else {
        int vlen = (int)strlen(val_raw);
        if (vlen > remaining) { a->overflow = 1; return; }
        memcpy(a->buf + a->len, val_raw, vlen);
        a->len += vlen;
    }
}

void attrs_init(attrs_t *a) {
    a->len = 0;
    a->overflow = 0;
    a->buf[0] = '\0';
}

void attrs_str(attrs_t *a, const char *key, const char *val) {
    attrs_append(a, key, val ? val : "", 0);
}

void attrs_int(attrs_t *a, const char *key, long long val) {
    char tmp[32];
    snprintf(tmp, sizeof(tmp), "%lld", val);
    attrs_append(a, key, tmp, 1);
}

void attrs_uint(attrs_t *a, const char *key, unsigned long long val) {
    char tmp[32];
    snprintf(tmp, sizeof(tmp), "%llu", val);
    attrs_append(a, key, tmp, 1);
}

void attrs_hex(attrs_t *a, const char *key, unsigned long long val) {
    char tmp[32];
    snprintf(tmp, sizeof(tmp), "0x%llx", val);
    attrs_append(a, key, tmp, 1);
}

void attrs_fmt(attrs_t *a, const char *key, const char *fmt, ...) {
    char tmp[512];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(tmp, sizeof(tmp), fmt, ap);
    va_end(ap);
    attrs_append(a, key, tmp, 0);
}

void emit(log_level_t level, const char *event_name, const attrs_t *a,
          const char *human_fmt, ...) {
    if (!log_level_visible(level)) return;

    fprintf(stdout, "@evt event=%s level=%s", event_name, level_name(level));

    if (human_fmt) {
        char buf[1024];
        va_list ap;
        va_start(ap, human_fmt);
        vsnprintf(buf, sizeof(buf), human_fmt, ap);
        va_end(ap);

        // msg= always quoted — human strings usually contain spaces.
        char qbuf[1280];
        int qn = append_quoted(qbuf, (int)sizeof(qbuf), buf);
        fprintf(stdout, " msg=%.*s", qn, qbuf);
    }

    if (a && a->len > 0) {
        fputc(' ', stdout);
        fwrite(a->buf, 1, (size_t)a->len, stdout);
        if (a->overflow) fputs(" _truncated=1", stdout);
    }

    fputc('\n', stdout);
    fflush(stdout);
}

// Free-form helpers wrap emit() as event="log". Same level filter; same
// output channel. No stderr duplication.
static void free_form(log_level_t level, const char *fmt, va_list ap) {
    if (!log_level_visible(level)) return;
    char buf[1024];
    vsnprintf(buf, sizeof(buf), fmt, ap);
    emit(level, "log", NULL, "%s", buf);
}

void dbg(const char *fmt, ...) { va_list ap; va_start(ap, fmt); free_form(LOG_DEBUG, fmt, ap); va_end(ap); }
void inf(const char *fmt, ...) { va_list ap; va_start(ap, fmt); free_form(LOG_INFO,  fmt, ap); va_end(ap); }
void wrn(const char *fmt, ...) { va_list ap; va_start(ap, fmt); free_form(LOG_WARN,  fmt, ap); va_end(ap); }
void er (const char *fmt, ...) { va_list ap; va_start(ap, fmt); free_form(LOG_ERROR, fmt, ap); va_end(ap); }

const char *human_bytes(unsigned long long n) {
    static __thread char buf[32];
    const char *units[] = { "B", "KB", "MB", "GB", "TB" };
    double v = (double)n;
    int u = 0;
    while (v >= 1024.0 && u < 4) { v /= 1024.0; u++; }
    if (u == 0) snprintf(buf, sizeof(buf), "%llu B", n);
    else        snprintf(buf, sizeof(buf), "%.1f %s", v, units[u]);
    return buf;
}
