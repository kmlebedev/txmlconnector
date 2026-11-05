/* C ABI test for libtxmlconnector: dlopen/LoadLibrary, all 9 exports,
 * error calls, callback and memory ownership. Built/run by "make cabi-test". */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "txmlconnector.h"

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#include <unistd.h>
#endif

typedef BYTE* (*fn_init)(const BYTE*, int);
typedef BYTE* (*fn_init_ex)(const BYTE*);
typedef BYTE* (*fn_uninit)(void);
typedef bool (*fn_set_cb)(TXC_CALLBACK);
typedef bool (*fn_set_cb_ex)(TXC_CALLBACK_EX, void*);
typedef BYTE* (*fn_send)(BYTE*);
typedef bool (*fn_free)(BYTE*);
typedef BYTE* (*fn_set_level)(int);
typedef int (*fn_info)(const BYTE*, BYTE**);

static volatile int callbacks = 0;
static volatile int ex_callbacks = 0;
static fn_free g_free = NULL;

static bool on_message(BYTE* message) {
    (void)message;
    callbacks++;
    return true;
}
static bool on_message_ex(BYTE* message, void* user) {
    (void)message; (void)user;
    ex_callbacks++;
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
static void check(BYTE* p, int want_null, const char* what) {
    if (want_null ? p != NULL : p == NULL) {
        fprintf(stderr, "%s: unexpected return %p\n", what, (void*)p);
        exit(3);
    }
    if (p && g_free) g_free(p);
}

int main(int argc, char** argv) {
    if (argc < 2) { fprintf(stderr, "usage: %s LIBRARY\n", argv[0]); return 2; }
    void* h = open_lib(argv[1]);
    fn_init Initialize = (fn_init)sym(h, "Initialize");
    fn_init_ex InitializeEx = (fn_init_ex)sym(h, "InitializeEx");
    fn_uninit UnInitialize = (fn_uninit)sym(h, "UnInitialize");
    fn_set_cb SetCallback = (fn_set_cb)sym(h, "SetCallback");
    fn_set_cb_ex SetCallbackEx = (fn_set_cb_ex)sym(h, "SetCallbackEx");
    fn_send SendCommand = (fn_send)sym(h, "SendCommand");
    fn_free FreeMemory = (fn_free)sym(h, "FreeMemory");
    fn_set_level SetLogLevel = (fn_set_level)sym(h, "SetLogLevel");
    fn_info GetServiceInfo = (fn_info)sym(h, "GetServiceInfo");
    g_free = FreeMemory;

    /* Error path before Initialize. */
    BYTE* before = SendCommand((BYTE*)"<command id=\"get_connector_version\"/>");
    check(before, 0, "SendCommand before Initialize");
    FreeMemory(NULL); /* must be safe; return value semantics are DLL-pending */
    if (SetCallback(on_message)) { fprintf(stderr, "SetCallback before Initialize must fail\n"); return 4; }

    /* Initialize, double Initialize, callback and command. */
    check(Initialize(NULL, 1), 1, "Initialize");
    BYTE* twice = Initialize(NULL, 1);
    if (twice == NULL) { fprintf(stderr, "second Initialize must fail\n"); return 4; }
    FreeMemory(twice);
    if (!SetCallback(on_message)) { fprintf(stderr, "SetCallback failed\n"); return 4; }
    BYTE* cmd = SendCommand((BYTE*)"<command id=\"get_connector_version\"/>");
    if (cmd == NULL || strstr((char*)cmd, "success=\"true\"") == NULL) {
        fprintf(stderr, "get_connector_version failed: %s\n", cmd ? (char*)cmd : "NULL");
        return 4;
    }
    FreeMemory(cmd);
    for (int i = 0; i < 100 && callbacks == 0; i++) {
#ifdef _WIN32
        Sleep(20);
#else
        usleep(20000);
#endif
    }
    if (callbacks == 0) { fprintf(stderr, "callback thread delivered nothing\n"); return 4; }
    if (!SetCallbackEx(on_message_ex, NULL)) { fprintf(stderr, "SetCallbackEx failed\n"); return 4; }
    BYTE* cmd2 = SendCommand((BYTE*)"<command id=\"get_connector_version\"/>");
    if (cmd2 == NULL) { fprintf(stderr, "second get_connector_version failed\n"); return 4; }
    FreeMemory(cmd2);
    for (int i = 0; i < 100 && ex_callbacks == 0; i++) {
#ifdef _WIN32
        Sleep(20);
#else
        usleep(20000);
#endif
    }
    if (ex_callbacks == 0) { fprintf(stderr, "SetCallbackEx callback delivered nothing\n"); return 4; }

    /* SetLogLevel: 1..3 valid (NULL), outside -> placeholder error string. */
    check(SetLogLevel(1), 1, "SetLogLevel(1)");
    check(SetLogLevel(9), 0, "SetLogLevel(9)");
    /* GetServiceInfo: NULL request -> code 4; version request -> code 0 + XML. */
    BYTE* info = NULL;
    if (GetServiceInfo(NULL, &info) != 4) { fprintf(stderr, "GetServiceInfo(NULL) != 4\n"); return 4; }
    info = NULL;
    int rc = GetServiceInfo((const BYTE*)"<request><value>version</value></request>", &info);
    if (rc != 0 || info == NULL || strstr((char*)info, "service_info") == NULL) {
        fprintf(stderr, "GetServiceInfo(version) rc=%d info=%p\n", rc, (void*)info);
        return 4;
    }
    FreeMemory(info);

    check(UnInitialize(), 1, "UnInitialize");
    check(UnInitialize(), 1, "second UnInitialize");
    BYTE* after = SendCommand((BYTE*)"<command id=\"get_connector_version\"/>");
    check(after, 0, "SendCommand after UnInitialize");
    /* Library stays loaded (Go c-shared cannot dlclose safely). */
    printf("C ABI OK: 9 exports, error paths, callback and FreeMemory\n");
    return 0;
}
