/* C5 conformance harness: replays ordered C-ABI scenarios against our drop-in
 * library and compares the returned UTF-8 strings byte-for-byte with the texts
 * recorded from the reference DLL (cmd/connector/testdata/cabi_scenarios.json,
 * generated from cabi-reference.json).
 *
 * Local only: no network, no server. Usage:
 *   abi_conformance LIBRARY SCENARIOS.json
 * Exit code 0 = all steps passed, 1 = diffs, 2 = usage/load error.
 * Built/run by "make cabi-conformance" and the compat-release workflow.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdbool.h>

/* The function names below are resolved with dlsym at run time, so the public
 * header cannot be included here (it declares them as extern functions).
 * These types must stay in sync with cmd/connector/include/txmlconnector.h. */
typedef unsigned char BYTE;
typedef bool (*TXC_CALLBACK)(BYTE*);
typedef bool (*TXC_CALLBACK_EX)(BYTE*, void*);

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#include <unistd.h>
#include <pthread.h>
#endif

typedef BYTE* (*fn_init)(const BYTE*, int);
typedef BYTE* (*fn_init_ex)(const BYTE*);
typedef BYTE* (*fn_uninit)(void);
typedef bool  (*fn_set_cb)(TXC_CALLBACK);
typedef bool  (*fn_set_cb_ex)(TXC_CALLBACK_EX, void*);
typedef BYTE* (*fn_send)(BYTE*);
typedef bool  (*fn_free)(BYTE*);
typedef BYTE* (*fn_set_level)(int);
typedef int   (*fn_info)(const BYTE*, BYTE**);

static fn_init Initialize;
static fn_init_ex InitializeEx;
static fn_uninit UnInitialize;
static fn_set_cb SetCallback;
static fn_set_cb_ex SetCallbackEx;
static fn_send SendCommand;
static fn_free FreeMemory;
static fn_set_level SetLogLevel;
static fn_info GetServiceInfo;

static char g_log_dir[512];
static BYTE* g_last_alloc = NULL;
static int g_fail = 0;
static int g_pass = 0;
static int g_step_ok = 0;
static int g_step_fail = 0;
static int g_step_no = 0;
static unsigned long g_main_thread = 0;
static char g_cb_text[16384];
static volatile int g_cb_count = 0;
static volatile unsigned long g_cb_thread = 0;
static volatile long g_cb_user = 0;

#define CH_LBRACE 123
#define CH_RBRACE 125
#define CH_LBRACKET 91
#define CH_RBRACKET 93
#define CH_QUOTE 34
#define CH_BACKSLASH 92
#define CH_COLON 58
#define CH_COMMA 44
#define CH_SLASH 47

static void emit_nl(void) { putchar(10); }

static void say(const char* a, const char* b) {
    fputs(a, stdout);
    if (b) fputs(b, stdout);
    emit_nl();
}

static unsigned long thread_id(void) {
#ifdef _WIN32
    return (unsigned long)GetCurrentThreadId();
#else
    return (unsigned long)pthread_self();
#endif
}

static void sleep_ms(long ms) {
    if (ms <= 0) return;
#ifdef _WIN32
    Sleep((DWORD)ms);
#else
    usleep((useconds_t)(ms * 1000));
#endif
}

static void close_lib(void* h) {
#ifdef _WIN32
    (void)h;
#else
    /* A Go c-shared library is not unloaded here on purpose: the conformance run
     * is a short-lived process and the reference contract keeps the library in
     * the address space until exit (docs/COMPATIBILITY.md, decision 6). */
    (void)h;
#endif
}

static bool collect_cb(BYTE* message) {
    size_t n = 0;
    if (message) {
        while (message[n] && n + 1 < sizeof(g_cb_text)) {
            g_cb_text[n] = (char)message[n];
            n++;
        }
    }
    g_cb_text[n] = 0;
    g_cb_thread = thread_id();
    g_cb_count++;
    if (FreeMemory) FreeMemory(message);
    return true;
}

static bool collect_cb_ex(BYTE* message, void* user) {
    long u = (long)(intptr_t)user;
    g_cb_user = u;
    return collect_cb(message);
}

/* ---------------- minimal flat JSON parser (strings and numbers) --------- */

typedef struct { const char* p; } JP;

static void jp_ws(JP* j) {
    while (*j->p == 32 || *j->p == 9 || *j->p == 10 || *j->p == 13) j->p++;
}

static void append_utf8(char* out, size_t* o, unsigned cp) {
    if (cp < 128) { out[(*o)++] = (char)cp; }
    else if (cp < 2048) {
        out[(*o)++] = (char)(192 | (cp >> 6));
        out[(*o)++] = (char)(128 | (cp & 63));
    } else {
        out[(*o)++] = (char)(224 | (cp >> 12));
        out[(*o)++] = (char)(128 | ((cp >> 6) & 63));
        out[(*o)++] = (char)(128 | (cp & 63));
    }
}

static int hexval(char c) {
    if (c >= 48 && c <= 57) return c - 48;
    if (c >= 97 && c <= 102) return c - 87;
    if (c >= 65 && c <= 70) return c - 55;
    return -1;
}

static char* jp_str(JP* j) {
    if (*j->p != CH_QUOTE) return NULL;
    j->p++;
    size_t cap = strlen(j->p) + 1;
    char* out = (char*)malloc(cap);
    size_t o = 0;
    while (*j->p && *j->p != CH_QUOTE) {
        char c = *j->p++;
        if (c == CH_BACKSLASH) {
            char e = *j->p++;
            int h1, h2, h3, h4;
            switch (e) {
            case 110: out[o++] = 10; break;
            case 116: out[o++] = 9; break;
            case 114: out[o++] = 13; break;
            case 98: out[o++] = 8; break;
            case 102: out[o++] = 12; break;
            case CH_SLASH: out[o++] = CH_SLASH; break;
            case CH_QUOTE: out[o++] = CH_QUOTE; break;
            case CH_BACKSLASH: out[o++] = CH_BACKSLASH; break;
            case 117:
                h1 = hexval(j->p[0]); h2 = hexval(j->p[1]);
                h3 = hexval(j->p[2]); h4 = hexval(j->p[3]);
                if (h1 >= 0 && h2 >= 0 && h3 >= 0 && h4 >= 0) {
                    unsigned cp = (unsigned)((h1 << 12) | (h2 << 8) | (h3 << 4) | h4);
                    append_utf8(out, &o, cp);
                }
                j->p += 4;
                break;
            default: out[o++] = e; break;
            }
        } else {
            out[o++] = c;
        }
    }
    if (*j->p == CH_QUOTE) j->p++;
    out[o] = 0;
    return out;
}

static void jp_skip_value(JP* j);

static void jp_skip_container(JP* j, char open_ch) {
    (void)open_ch;
    while (*j->p) {
        if (*j->p == CH_QUOTE) { char* s = jp_str(j); free(s); continue; }
        if (*j->p == CH_LBRACE || *j->p == CH_LBRACKET) { jp_skip_value(j); continue; }
        if (*j->p == CH_RBRACE || *j->p == CH_RBRACKET) { j->p++; return; }
        if (*j->p == CH_COMMA || *j->p == CH_COLON) { j->p++; continue; }
        j->p++;
    }
}

static void jp_skip_value(JP* j) {
    jp_ws(j);
    if (*j->p == CH_QUOTE) { char* s = jp_str(j); free(s); return; }
    if (*j->p == CH_LBRACE) { j->p++; jp_skip_container(j, CH_LBRACE); return; }
    if (*j->p == CH_LBRACKET) { j->p++; jp_skip_container(j, CH_LBRACKET); return; }
    while (*j->p && *j->p != CH_COMMA && *j->p != CH_RBRACE && *j->p != CH_RBRACKET) j->p++;
}

static int jp_number(JP* j, long* out) {
    char* end = NULL;
    jp_ws(j);
    long v = strtol(j->p, &end, 10);
    if (end == j->p) return 0;
    j->p = end;
    *out = v;
    return 1;
}

static int jp_bool(JP* j, int* out) {
    jp_ws(j);
    if (strncmp(j->p, "true", 4) == 0) { j->p += 4; *out = 1; return 1; }
    if (strncmp(j->p, "false", 5) == 0) { j->p += 5; *out = 0; return 1; }
    if (strncmp(j->p, "null", 4) == 0) { j->p += 4; *out = 0; return 1; }
    return 0;
}

static int key_is(const char* key, const char* lit) {
    return key && strcmp(key, lit) == 0;
}

typedef struct {
    char* id;
    char* call;
    char* args;
    char* expect;
    char* text;
    char* resp;
    char* callback;
    char* callback_user;
    long code;
    int has_code;
    int resp_null;
    long ms;
} Step;

static void parse_step(JP* j, Step* s) {
    memset(s, 0, sizeof(*s));
    jp_ws(j);
    if (*j->p != CH_LBRACE) return;
    j->p++;
    for (;;) {
        jp_ws(j);
        if (*j->p == CH_RBRACE) { j->p++; return; }
        char* key = jp_str(j);
        jp_ws(j);
        if (*j->p == CH_COLON) j->p++;
        jp_ws(j);
        if (key_is(key, "id")) s->id = jp_str(j);
        else if (key_is(key, "call")) s->call = jp_str(j);
        else if (key_is(key, "args")) s->args = jp_str(j);
        else if (key_is(key, "expect")) s->expect = jp_str(j);
        else if (key_is(key, "text")) s->text = jp_str(j);
        else if (key_is(key, "resp")) s->resp = jp_str(j);
        else if (key_is(key, "callback")) s->callback = jp_str(j);
        else if (key_is(key, "callback_user")) s->callback_user = jp_str(j);
        else if (key_is(key, "code")) { s->has_code = jp_number(j, &s->code); }
        else if (key_is(key, "ms")) { jp_number(j, &s->ms); }
        else if (key_is(key, "resp_null")) { jp_bool(j, &s->resp_null); }
        else jp_skip_value(j);
        free(key);
        jp_ws(j);
        if (*j->p == CH_COMMA) { j->p++; continue; }
        if (*j->p == CH_RBRACE) { j->p++; return; }
        return;
    }
}

static Step* parse_scenarios(const char* json, int* count) {
    JP j;
    j.p = json;
    Step* steps = NULL;
    int cap = 0;
    *count = 0;
    jp_ws(&j);
    if (*j.p != CH_LBRACE) return NULL;
    j.p++;
    for (;;) {
        jp_ws(&j);
        if (*j.p == CH_RBRACE) { j.p++; break; }
        char* key = jp_str(&j);
        jp_ws(&j);
        if (*j.p == CH_COLON) j.p++;
        jp_ws(&j);
        if (key_is(key, "steps") && *j.p == CH_LBRACKET) {
            j.p++;
            for (;;) {
                jp_ws(&j);
                if (*j.p == CH_RBRACKET) { j.p++; break; }
                if (*j.p == CH_LBRACE) {
                    if (*count == cap) {
                        cap = cap ? cap * 2 : 16;
                        steps = (Step*)realloc(steps, sizeof(Step) * (size_t)cap);
                    }
                    parse_step(&j, &steps[*count]);
                    (*count)++;
                } else {
                    jp_skip_value(&j);
                }
                jp_ws(&j);
                if (*j.p == CH_COMMA) { j.p++; continue; }
                if (*j.p == CH_RBRACKET) { j.p++; break; }
            }
        } else {
            jp_skip_value(&j);
        }
        free(key);
        jp_ws(&j);
        if (*j.p == CH_COMMA) { j.p++; continue; }
        if (*j.p == CH_RBRACE) { j.p++; break; }
    }
    return steps;
}

/* ---------------- scenario replay helpers -------------------------------- */

static char* read_file(const char* path) {
    FILE* f = fopen(path, "rb");
    if (!f) return NULL;
    if (fseek(f, 0, SEEK_END) != 0) { fclose(f); return NULL; }
    long n = ftell(f);
    if (n < 0) { fclose(f); return NULL; }
    rewind(f);
    char* buf = (char*)malloc((size_t)n + 1);
    if (!buf) { fclose(f); return NULL; }
    size_t got = fread(buf, 1, (size_t)n, f);
    fclose(f);
    buf[got] = 0;
    return buf;
}

static char* subst_log_dir(const char* in) {
    if (!in) return NULL;
    size_t cap = strlen(in) + 1024;
    char* out = (char*)malloc(cap);
    size_t o = 0;
    const char* p = in;
    const char* token = "{LOG_DIR}";
    size_t tl = strlen(token);
    while (*p) {
        if (strncmp(p, token, tl) == 0) {
            size_t dl = strlen(g_log_dir);
            if (o + dl + 1 >= cap) break;
            memcpy(out + o, g_log_dir, dl);
            o += dl;
            p += tl;
        } else {
            out[o++] = *p++;
        }
    }
    out[o] = 0;
    return out;
}

static void print_bytes_hex(const char* label, const char* s) {
    fputs(label, stdout);
    if (!s) { say(" <NULL>", NULL); return; }
    fputs(" [", stdout);
    for (size_t i = 0; s[i]; i++) printf("%02x", (unsigned char)s[i]);
    say("]", NULL);
}

static void fail_text(const Step* s, const char* actual, const char* what) {
    g_fail++;
    printf("FAIL %s: %s", s->id ? s->id : "?", what);
    emit_nl();
    if (s->text) { fputs("  expected: ", stdout); fputs(s->text, stdout); emit_nl(); }
    fputs("  actual:   ", stdout);
    if (actual) fputs(actual, stdout); else fputs("<NULL>", stdout);
    emit_nl();
    print_bytes_hex("  expected bytes:", s->text);
    print_bytes_hex("  actual bytes:  ", actual);
}

static int expect_string(const Step* s, char* actual) {
    if (!actual) { fail_text(s, NULL, "expected a string, got NULL"); return 0; }
    if (s->text && strcmp(actual, s->text) != 0) { fail_text(s, actual, "string mismatch"); return 0; }
    g_pass++;
    printf("PASS %s", s->id ? s->id : "?");
    emit_nl();
    return 1;
}

static void expect_ok(const Step* s) {
    g_pass++;
    printf("PASS %s", s->id ? s->id : "?");
    emit_nl();
}

static void wait_callback(const Step* s) {
    if (!s->callback) return;
    for (int i = 0; i < 300; i++) {
        if (g_cb_count > 0 && strcmp(g_cb_text, s->callback) == 0) break;
        sleep_ms(10);
    }
    if (g_cb_count <= 0) {
        g_fail++;
        printf("FAIL %s: callback not delivered", s->id ? s->id : "?");
        emit_nl();
        fputs("  expected: ", stdout);
        fputs(s->callback, stdout);
        emit_nl();
        return;
    }
    if (strcmp(g_cb_text, s->callback) != 0) {
        g_fail++;
        printf("FAIL %s: callback text mismatch", s->id ? s->id : "?");
        emit_nl();
        fputs("  expected: ", stdout);
        fputs(s->callback, stdout);
        emit_nl();
        fputs("  actual:   ", stdout);
        fputs(g_cb_text, stdout);
        emit_nl();
        return;
    }
    if (g_cb_thread == g_main_thread) {
        g_fail++;
        printf("FAIL %s: callback delivered on the caller thread", s->id ? s->id : "?");
        emit_nl();
        return;
    }
    if (s->callback_user) {
        long want = strtol(s->callback_user, NULL, 0);
        if (g_cb_user != want) {
            g_fail++;
            printf("FAIL %s: callback user data mismatch (want %ld got %ld)", s->id ? s->id : "?", want, g_cb_user);
            emit_nl();
            return;
        }
    }
    expect_ok(s);
}

static void reset_callback(void) {
    g_cb_text[0] = 0;
    g_cb_count = 0;
    g_cb_user = 0;
}

static int run_step(Step* s) {
    char* arg = subst_log_dir(s->args);
    g_step_no++;
    reset_callback();
    if (key_is(s->call, "sleep")) {
        sleep_ms(s->ms);
        return 0;
    }
    if (key_is(s->call, "UnInitialize")) {
        BYTE* r = UnInitialize();
        if (s->expect && strcmp(s->expect, "null") == 0) {
            if (r) { fail_text(s, (char*)r, "expected NULL"); FreeMemory(r); }
            else expect_ok(s);
        } else if (r) expect_string(s, (char*)r);
        else fail_text(s, NULL, "expected an error string, got NULL");
        if (r) FreeMemory(r);
        free(arg);
        return 0;
    }
    if (key_is(s->call, "Initialize")) {
        char* path = NULL;
        int level = 1;
        if (arg) {
            char* comma = strrchr(arg, CH_COMMA);
            if (comma) {
                int lv = atoi(comma + 1);
                level = lv;
                *comma = 0;
            }
            if (arg[0] && strcmp(arg, "NULL") != 0) path = arg;
        }
        BYTE* r = Initialize((const BYTE*)path, level);
        if (s->expect && strcmp(s->expect, "null") == 0) {
            if (r) { fail_text(s, (char*)r, "expected NULL"); FreeMemory(r); }
            else expect_ok(s);
        } else if (r) expect_string(s, (char*)r);
        else fail_text(s, NULL, "expected an error string, got NULL");
        if (r) FreeMemory(r);
        free(arg);
        return 0;
    }
    if (key_is(s->call, "InitializeEx")) {
        const BYTE* xml = NULL;
        if (arg && arg[0] && strcmp(arg, "NULL") != 0) xml = (const BYTE*)arg;
        BYTE* r = InitializeEx(xml);
        if (s->expect && strcmp(s->expect, "null") == 0) {
            if (r) { fail_text(s, (char*)r, "expected NULL"); FreeMemory(r); }
            else expect_ok(s);
        } else if (r) expect_string(s, (char*)r);
        else fail_text(s, NULL, "expected an error string, got NULL");
        if (r) FreeMemory(r);
        free(arg);
        return 0;
    }
    if (key_is(s->call, "SetCallback")) {
        bool ok = SetCallback(collect_cb);
        if (s->expect && strcmp(s->expect, "true") == 0) { if (ok) expect_ok(s); else fail_text(s, "false", "expected true"); }
        else fail_text(s, ok ? "true" : "false", "expected false");
        free(arg);
        return 0;
    }
    if (key_is(s->call, "SetCallbackEx")) {
        void* user = (void*)(intptr_t)(arg && arg[0] ? strtol(arg, NULL, 0) : 0);
        bool ok = SetCallbackEx(collect_cb_ex, user);
        if (s->expect && strcmp(s->expect, "true") == 0) { if (ok) expect_ok(s); else fail_text(s, "false", "expected true"); }
        else fail_text(s, ok ? "true" : "false", "expected false");
        free(arg);
        return 0;
    }
    if (key_is(s->call, "SetLogLevel")) {
        int level = arg ? atoi(arg) : 0;
        BYTE* r = SetLogLevel(level);
        if (s->expect && strcmp(s->expect, "null") == 0) {
            if (r) { fail_text(s, (char*)r, "expected NULL"); FreeMemory(r); }
            else expect_ok(s);
        } else if (r) expect_string(s, (char*)r);
        else fail_text(s, NULL, "expected an error string, got NULL");
        if (r) FreeMemory(r);
        free(arg);
        return 0;
    }
    if (key_is(s->call, "FreeMemory")) {
        BYTE* target = NULL;
        if (arg && strcmp(arg, "last") == 0) { target = g_last_alloc; g_last_alloc = NULL; }
        else if (arg && strcmp(arg, "NULL") == 0) target = NULL;
        bool ok = FreeMemory(target);
        if (s->expect && strcmp(s->expect, "true") == 0) { if (ok) expect_ok(s); else fail_text(s, "false", "expected true"); }
        else fail_text(s, ok ? "true" : "false", "expected false");
        free(arg);
        return 0;
    }
    if (key_is(s->call, "SendCommand")) {
        BYTE* r;
        if (arg && strcmp(arg, "NULL") == 0) r = SendCommand(NULL);
        else r = SendCommand((BYTE*)arg);
        if (r) {
            int ok = expect_string(s, (char*)r);
            free(arg);
            g_last_alloc = r; /* freed by a later FreeMemory("last") step */
            if (!ok) return 0;
        } else {
            fail_text(s, NULL, "expected a string, got NULL");
            free(arg);
            return 0;
        }
        wait_callback(s);
        return 0;
    }
    if (key_is(s->call, "GetServiceInfo")) {
        BYTE* req = NULL;
        int resp_null_arg = 0;
        if (arg && strncmp(arg, "!respnull:", 10) == 0) { resp_null_arg = 1; req = (BYTE*)(arg + 10); }
        else if (arg && strcmp(arg, "NULL") != 0) req = (BYTE*)arg;
        BYTE* resp = NULL;
        int code = GetServiceInfo(req, resp_null_arg ? NULL : &resp);
        int want_code = s->has_code ? (int)s->code : 0;
        if (code != want_code) {
            g_fail++;
            printf("FAIL %s: GetServiceInfo code mismatch (want %d got %d)", s->id ? s->id : "?", want_code, code);
            emit_nl();
            if (resp) { print_bytes_hex("  resp bytes:", (char*)resp); FreeMemory(resp); }
            free(arg);
            return 0;
        }
        if (s->resp_null || s->has_code == 0) {
            if (resp) {
                g_fail++;
                printf("FAIL %s: expected NULL response", s->id ? s->id : "?");
                emit_nl();
                FreeMemory(resp);
            } else expect_ok(s);
        } else if (resp) {
            expect_string(s, (char*)resp);
            g_last_alloc = resp;
        } else {
            fail_text(s, NULL, "expected a response string, got NULL");
        }
        free(arg);
        return 0;
    }
    ++g_fail;
    printf("FAIL step %d: unknown call", g_step_no);
    emit_nl();
    free(arg);
    return 0;
}

static void* open_library(const char* path) {
#ifdef _WIN32
    HMODULE h = LoadLibraryA(path);
#else
    void* h = dlopen(path, RTLD_NOW);
#endif
    return (void*)h;
}

static void* resolve(void* h, const char* name) {
#ifdef _WIN32
    void* p = (void*)GetProcAddress((HMODULE)h, name);
#else
    void* p = dlsym(h, name);
#endif
    if (!p) {
        fputs("missing export: ", stderr);
        fputs(name, stderr);
        emit_nl();
        exit(2);
    }
    return p;
}

int main(int argc, char** argv) {
    if (argc < 3) {
        say("usage: abi_conformance LIBRARY SCENARIOS.json", NULL);
        return 2;
    }
    setvbuf(stdout, NULL, _IONBF, 0);
    setvbuf(stderr, NULL, _IONBF, 0);
    g_main_thread = thread_id();
    void* h = open_library(argv[1]);
    if (!h) { say("cannot open library", NULL); return 2; }
    Initialize = (fn_init)resolve(h, "Initialize");
    InitializeEx = (fn_init_ex)resolve(h, "InitializeEx");
    UnInitialize = (fn_uninit)resolve(h, "UnInitialize");
    SetCallback = (fn_set_cb)resolve(h, "SetCallback");
    SetCallbackEx = (fn_set_cb_ex)resolve(h, "SetCallbackEx");
    SendCommand = (fn_send)resolve(h, "SendCommand");
    FreeMemory = (fn_free)resolve(h, "FreeMemory");
    SetLogLevel = (fn_set_level)resolve(h, "SetLogLevel");
    GetServiceInfo = (fn_info)resolve(h, "GetServiceInfo");

#ifdef _WIN32
    {
        char tmpbuf[MAX_PATH];
        DWORD n = GetTempPathA((DWORD)sizeof(tmpbuf), tmpbuf);
        if (n == 0 || n >= sizeof(tmpbuf)) {
            snprintf(tmpbuf, sizeof(tmpbuf), ".");
        }
        snprintf(g_log_dir, sizeof(g_log_dir), "%scabi-conf-%lu", tmpbuf, (unsigned long)GetCurrentProcessId());
        CreateDirectoryA(g_log_dir, NULL);
    }
#else
    {
        const char* tmp = getenv("TMPDIR");
        if (!tmp || !tmp[0]) tmp = "/tmp";
        snprintf(g_log_dir, sizeof(g_log_dir), "%s/cabi-conf-XXXXXX", tmp);
        if (!mkdtemp(g_log_dir)) {
            snprintf(g_log_dir, sizeof(g_log_dir), "%s/cabi-conf", tmp);
        }
    }
#endif

    char* json = read_file(argv[2]);
    if (!json) { say("cannot read scenarios file", NULL); return 2; }
    int count = 0;
    Step* steps = parse_scenarios(json, &count);
    free(json);
    if (!steps || count == 0) { say("no scenarios parsed", NULL); return 2; }
    printf("conformance: %d steps, log dir %s", count, g_log_dir);
    emit_nl();
    for (int i = 0; i < count; i++) {
        int fail_before = g_fail;
        run_step(&steps[i]);
        if (g_fail == fail_before) g_step_ok++;
        else g_step_fail++;
    }
    printf("conformance: %d/%d steps passed, %d failed", g_step_ok, count, g_step_fail);
    emit_nl();
    close_lib(h);
    return g_step_fail == 0 ? 0 : 1;
}
