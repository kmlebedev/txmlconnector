/* Live read-only C ABI test for libtxmlconnector (C4, task-72).
 * Env-gated: requires TC_HOST/TC_PORT/TC_LOGIN/TC_PASSWORD (source demo.env).
 * Sequence: Initialize -> SetCallback -> connect -> wait connected=true ->
 * get_securities -> disconnect -> UnInitialize. No trading commands. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include "txmlconnector.h"

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#include <unistd.h>
#endif

typedef BYTE* (*fn_init)(const BYTE*, int);
typedef BYTE* (*fn_uninit)(void);
typedef bool (*fn_set_cb)(TXC_CALLBACK);
typedef BYTE* (*fn_send)(BYTE*);
typedef bool (*fn_free)(BYTE*);

static volatile int callbacks = 0;
static volatile int connected = 0;
static volatile int disconnected = 0;
static fn_free g_free = NULL;
static BYTE* g_connect = NULL;
static BYTE* g_disconnect = NULL;
static BYTE* g_get_securities = NULL;

static bool on_message(BYTE* message) {
    callbacks++;
    if (message) {
        if (strstr((char*)message, "connected=\"true\"")) connected = 1;
        if (strstr((char*)message, "connected=\"false\"")) disconnected = 1;
    }
    return true;
}
static void* sym(void* h, const char* name) {
#ifdef _WIN32
    void* p = (void*)GetProcAddress((HMODULE)h, name);
#else
    void* p = dlsym(h, name);
#endif
    if (!p) { fprintf(stderr, "missing export %s\n", name); exit(2); }
    return p;
}
static void* open_lib(const char* path) {
#ifdef _WIN32
    HMODULE h = LoadLibraryA(path);
#else
    void* h = dlopen(path, RTLD_NOW);
#endif
    if (!h) { fprintf(stderr, "cannot open %s\n", path); exit(2); }
    return h;
}
static BYTE* make_command(const char* fmt, const char* a, const char* b, const char* c, const char* d) {
    char buf[4096];
    snprintf(buf, sizeof(buf), fmt, a, b, c, d);
    return (BYTE*)strdup(buf);
}
static void sleep_ms(int ms) {
#ifdef _WIN32
    Sleep(ms);
#else
    usleep(ms * 1000);
#endif
}
static int wait_flag(volatile int* flag, int timeout_ms) {
    for (int i = 0; i < timeout_ms / 50; i++) {
        if (*flag) return 1;
        sleep_ms(50);
    }
    return 0;
}

int main(int argc, char** argv) {
    const char* host = getenv("TC_HOST");
    const char* port = getenv("TC_PORT");
    const char* login = getenv("TC_LOGIN");
    const char* password = getenv("TC_PASSWORD");
    if (argc < 2 || !host || !port || !login || !password) {
        printf("SKIP: usage: abi_live LIBRARY with TC_HOST/TC_PORT/TC_LOGIN/TC_PASSWORD\n");
        return 0;
    }
    void* h = open_lib(argv[1]);
    fn_init Initialize = (fn_init)sym(h, "Initialize");
    fn_uninit UnInitialize = (fn_uninit)sym(h, "UnInitialize");
    fn_set_cb SetCallback = (fn_set_cb)sym(h, "SetCallback");
    fn_send SendCommand = (fn_send)sym(h, "SendCommand");
    fn_free FreeMemory = (fn_free)sym(h, "FreeMemory");
    g_free = FreeMemory;

    /* The DLL (and our library since C2) refuses Initialize(NULL, ...): pass a
     * log directory; TXC_LOG_DIR overrides the default under bin/. */
    const char* log_dir = getenv("TXC_LOG_DIR");
    if (!log_dir || !*log_dir) log_dir = "bin/abi-live-logs";
    const char* level_env = getenv("TXC_LOG_LEVEL");
    int level = level_env && *level_env ? atoi(level_env) : 1;
    BYTE* err = Initialize((const BYTE*)log_dir, level);
    if (err != NULL) { fprintf(stderr, "Initialize: %s\n", (char*)err); return 3; }
    if (!SetCallback(on_message)) { fprintf(stderr, "SetCallback failed\n"); return 3; }

    g_connect = make_command("<command id=\"connect\"><login>%s</login><password>%s</password><host>%s</host><port>%s</port></command>", login, password, host, port);
    BYTE* result = SendCommand(g_connect);
    if (!result || strstr((char*)result, "success=\"true\"") == NULL) {
        fprintf(stderr, "connect result: %s\n", result ? (char*)result : "NULL"); return 3;
    }
    FreeMemory(result);
    if (!wait_flag(&connected, 60000)) { fprintf(stderr, "no connected=true within 60s\n"); return 3; }
    printf("connected=true after %d callbacks\n", callbacks);

    g_get_securities = (BYTE*)strdup("<command id=\"get_securities\"/>");
    result = SendCommand(g_get_securities);
    if (!result || strstr((char*)result, "success=\"true\"") == NULL) {
        fprintf(stderr, "get_securities result: %s\n", result ? (char*)result : "NULL"); return 3;
    }
    FreeMemory(result);

    g_disconnect = (BYTE*)strdup("<command id=\"disconnect\"/>");
    result = SendCommand(g_disconnect);
    if (!result || strstr((char*)result, "success=\"true\"") == NULL) {
        fprintf(stderr, "disconnect result: %s\n", result ? (char*)result : "NULL"); return 3;
    }
    FreeMemory(result);
    if (!wait_flag(&disconnected, 10000)) { fprintf(stderr, "no connected=false after disconnect\n"); return 3; }
    free(g_connect); free(g_disconnect); free(g_get_securities);
    check_uninit:
    err = UnInitialize();
    if (err != NULL) { fprintf(stderr, "UnInitialize: %s\n", (char*)err); return 3; }
    printf("LIVE ABI OK: connect -> connected=true -> get_securities -> disconnect -> UnInitialize; callbacks=%d\n", callbacks);
    return 0;
}
