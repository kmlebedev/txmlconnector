/* C ABI conformance test for libtxmlconnector (C2 exact lifecycle/texts). */
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
typedef BYTE* (*fn_init_ex)(const BYTE*);
typedef BYTE* (*fn_uninit)(void);
typedef bool (*fn_set_cb)(TXC_CALLBACK);
typedef BYTE* (*fn_send)(BYTE*);
typedef bool (*fn_free)(BYTE*);
typedef BYTE* (*fn_set_level)(int);
typedef int (*fn_info)(const BYTE*, BYTE**);

static volatile int callbacks = 0;
static fn_free g_free = NULL;
static bool on_message(BYTE* m) { (void)m; callbacks++; return false; }

static void* sym(void* h, const char* n) {
#ifdef _WIN32
    void* p = (void*)GetProcAddress((HMODULE)h, n);
#else
    void* p = dlsym(h, n);
#endif
    if (!p) { fprintf(stderr, "missing export %s\n", n); exit(2); }
    return p;
}
static void* open_lib(const char* p) {
#ifdef _WIN32
    HMODULE h = LoadLibraryA(p);
#else
    void* h = dlopen(p, RTLD_NOW);
#endif
    if (!h) { fprintf(stderr, "cannot open %s\n", p); exit(2); }
    return h;
}
static void expect(BYTE* p, const char* text, const char* what) {
    if (p == NULL) { fprintf(stderr, "%s: NULL, want %s\n", what, text); exit(3); }
    if (strcmp((char*)p, text) != 0) {
        fprintf(stderr, "%s: got %s, want %s\n", what, (char*)p, text); exit(3);
    }
    g_free(p);
}
static void expect_null(BYTE* p, const char* what) {
    if (p != NULL) { fprintf(stderr, "%s: got %s, want NULL\n", what, (char*)p); exit(3); }
}

int main(int argc, char** argv) {
    if (argc < 2) { fprintf(stderr, "usage: %s LIBRARY\n", argv[0]); return 2; }
    void* h = open_lib(argv[1]);
    fn_init Initialize = (fn_init)sym(h, "Initialize");
    fn_init_ex InitializeEx = (fn_init_ex)sym(h, "InitializeEx");
    fn_uninit UnInitialize = (fn_uninit)sym(h, "UnInitialize");
    fn_set_cb SetCallback = (fn_set_cb)sym(h, "SetCallback");
    fn_send SendCommand = (fn_send)sym(h, "SendCommand");
    fn_free FreeMemory = (fn_free)sym(h, "FreeMemory");
    fn_set_level SetLogLevel = (fn_set_level)sym(h, "SetLogLevel");
    fn_info GetServiceInfo = (fn_info)sym(h, "GetServiceInfo");
    g_free = FreeMemory;

    expect(SendCommand(NULL), "<error>Error document empty.</error>", "SendCommand(NULL)");
    expect(SendCommand((BYTE*)"<x/>"), "<error>Only &amp;lt;command&amp;gt; tags could be passed to SendCommand function</error>", "non-command root");
    expect(Initialize(NULL, 1), "<error>Не удалось инициализировать библиотеку. Не указан путь к директории для хранения лог-файлов</error>", "Initialize(NULL)");
    if (!SetCallback(NULL) || !SetCallback(on_message)) { fprintf(stderr, "SetCallback must return true\n"); return 4; }
    /* The log directory must exist before Initialize (O). */
#ifdef _WIN32
    char logs[600], tmp[MAX_PATH];
    GetTempPathA(sizeof(tmp), tmp);
    snprintf(logs, sizeof(logs), "%stxc-c2-%lu", tmp, (unsigned long)GetCurrentProcessId());
    CreateDirectoryA(logs, NULL);
#else
    char logs[] = "/tmp/txc-c2-XXXXXX";
    if (mkdtemp(logs) == NULL) { perror("mkdtemp"); return 4; }
#endif
    expect(Initialize(logs, 0), "<error>Не удалось инициализировать библиотеку. Wrong log level.</error>", "Initialize(level0)");
    expect_null(Initialize(logs, 2), "Initialize valid");
    expect(Initialize(logs, 2), "<error>Не удалось инициализировать библиотеку. Библиотека уже инициализирована</error>", "double Initialize");
    expect_null(UnInitialize(), "UnInitialize");
    expect(UnInitialize(), "<error>Библиотека не инициализирована</error>", "UnInitialize again");
    expect(SendCommand((BYTE*)"<command id=\"get_connector_version\"/>"), "<error>Библиотека не инициализована. Необходимо выполнить функцию Initialize()</error>", "SendCommand after uninit");
    expect(SetLogLevel(0), "<error>Invalid log level. Should be in range [1,3]</error>", "SetLogLevel(0)");
    expect_null(SetLogLevel(2), "SetLogLevel(2)");
    if (!FreeMemory(NULL)) { fprintf(stderr, "FreeMemory(NULL) must be true\n"); return 4; }

    /* InitializeEx exact errors + valid init. */
    expect(InitializeEx(NULL), "<error>Не удалось инициализировать библиотеку. параметр функции равен нулю</error>", "InitializeEx(NULL)");
    expect(InitializeEx((const BYTE*)"<init log_path>"), "<error>Не удалось инициализировать библиотеку. Error reading Attributes.</error>", "InitializeEx broken");
    expect(InitializeEx((const BYTE*)"<root/>"), "<error>Не удалось инициализировать библиотеку. не найден элемент &lt;init&gt;</error>", "InitializeEx no init");
    expect(InitializeEx((const BYTE*)"<init/>"), "<error>Не удалось инициализировать библиотеку. не найден обязательный атрибут &lt;log_path&gt;</error>", "InitializeEx no path");
    expect(InitializeEx((const BYTE*)"<init log_path=\"x\"/>"), "<error>Не удалось инициализировать библиотеку. не найден обязательный атрибут &lt;log_level&gt;</error>", "InitializeEx no level");
    expect(InitializeEx((const BYTE*)"<init log_path=\"x\" log_level=\"4\"/>"), "<error>Не удалось инициализировать библиотеку. Wrong log level.</error>", "InitializeEx bad level");
    /* The log directory must exist (O: the DLL does not create it). */
    expect(Initialize((const BYTE*)"/nonexistent-txc-logs", 2), "<error>Не удалось инициализировать библиотеку. Указанный путь не существует</error>", "Initialize missing dir");
    expect(InitializeEx((const BYTE*)"<init log_path=\"/nonexistent-txc-logs\" log_level=\"2\"/>"), "<error>Не удалось инициализировать библиотеку. Указанный путь не существует</error>", "InitializeEx missing dir");
    char initex[600];
    snprintf(initex, sizeof(initex), "<init log_path=\"%s\" log_level=\"1\"/>", logs);
    expect_null(InitializeEx((const BYTE*)initex), "InitializeEx valid");
    expect_null(UnInitialize(), "UnInitialize after InitEx");
    expect(UnInitialize(), "<error>Библиотека не инициализирована</error>", "UnInitialize again after InitEx");

    /* GetServiceInfo codes/texts. The reference checks the 5000 ms rate limit
     * first and updates its timestamp before the parameter checks, so a limited
     * call returns 6 even for a NULL response/request; the timestamp is not
     * updated by limited calls (docs/protocol/cabi.md §9). */
    BYTE* resp = NULL;
    if (GetServiceInfo((const BYTE*)"<request><value>version</value></request>", NULL) != 5) { fprintf(stderr, "GetServiceInfo(resp NULL) != 5\n"); return 4; }
    if (GetServiceInfo(NULL, &resp) != 6) { fprintf(stderr, "rate limit != 6 before parameter checks\n"); return 4; }
#ifdef _WIN32
    Sleep(6100);
#else
    sleep(6);
#endif
    resp = NULL;
    if (GetServiceInfo(NULL, &resp) != 4 || resp != NULL) { fprintf(stderr, "GetServiceInfo(NULL) != 4\n"); return 4; }
#ifdef _WIN32
    Sleep(6100);
#else
    sleep(6);
#endif
    resp = NULL;
    int rc = GetServiceInfo((const BYTE*)"<request><value>version</value></request>", &resp);
    if (rc != 0) { fprintf(stderr, "GetServiceInfo(version) rc=%d\n", rc); return 4; }
    expect(resp, "<service_info><version>6.47.2.26.4</version></service_info>", "GetServiceInfo version"); resp = NULL;
    if (GetServiceInfo((const BYTE*)"<request><value>version</value></request>", &resp) != 6) { fprintf(stderr, "rate limit != 6\n"); return 4; }

    /* get_connector_version async callback (already uninitialized, so init again). */
    expect_null(Initialize(logs, 1), "Initialize for callback");
    if (!SetCallback(on_message)) { fprintf(stderr, "SetCallback true\n"); return 4; }
    expect(SendCommand((BYTE*)"<command id=\"get_connector_version\"/>"), "<result success=\"true\"/>", "get_connector_version");
    for (int i = 0; i < 100 && callbacks == 0; i++) {
#ifdef _WIN32
        Sleep(20);
#else
        usleep(20000);
#endif
    }
    if (callbacks == 0) { fprintf(stderr, "callback delivered nothing\n"); return 4; }
    expect_null(UnInitialize(), "final UnInitialize");

    /* Log files in the DLL format (docs/protocol/logs.md). */
    {
        char path[700], day[16], buf[65536];
        time_t now = time(NULL);
        strftime(day, sizeof(day), "%Y%m%d", localtime(&now));
        const char* need[][2] = {
            {"_dsp.log", "<info> Open log "},
            {"_dsp.log", "<info> Waiting for callback thread...\r\n"},
            {"_dsp.log", "<info> Shutdown OK\r\n"},
            {"_xdf.log", "<cmd> [V] System version 6.47. TXmlConnector version 2.26.4\r\n"},
            {"_xdf.log", "<cmd> [I] <command id=\"get_connector_version\"/>\r\n"},
            {"_xdf.log", "<res> [R] <result success=\"true\"/>\r\n"},
        };
        for (size_t i = 0; i < sizeof(need) / sizeof(need[0]); i++) {
            snprintf(path, sizeof(path), "%s/%s%s", logs, day, need[i][0]);
            FILE* f = fopen(path, "rb");
            if (!f) { fprintf(stderr, "log file %s missing\n", path); return 4; }
            size_t n = fread(buf, 1, sizeof(buf) - 1, f);
            fclose(f);
            buf[n] = 0;
            if (!strstr(buf, need[i][1])) { fprintf(stderr, "%s lacks %s\n", path, need[i][1]); return 4; }
        }
    }
    printf("C ABI conformance OK: lifecycle, exact texts, service info, callback, memory\n");
    return 0;
}
