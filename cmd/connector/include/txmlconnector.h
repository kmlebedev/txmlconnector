#ifndef TXMLCONNECTOR_H
#define TXMLCONNECTOR_H

/* Public C ABI of the native TXmlConnector-compatible library.
 * Mirrors the 9 exports of txmlconnector64-6.47.2.26.4.dll; exact wrapper
 * texts/codes for InitializeEx/GetServiceInfo/SetLogLevel are pending RE-2. */
#include <stdbool.h>

#ifdef _WIN32
#  define TXC_STDCALL __stdcall
#  ifdef TXC_BUILD
#    define TXC_API __declspec(dllexport)
#  else
#    define TXC_API __declspec(dllimport)
#  endif
#else
#  define TXC_STDCALL
#  define TXC_API
#endif

typedef unsigned char BYTE;
typedef bool (*TXC_CALLBACK)(BYTE*);
typedef bool (*TXC_CALLBACK_EX)(BYTE*, void*);

#ifdef __cplusplus
extern "C" {
#endif

TXC_API BYTE* TXC_STDCALL Initialize(const BYTE* log_path, int log_level);
TXC_API BYTE* TXC_STDCALL InitializeEx(const BYTE* xml);
TXC_API BYTE* TXC_STDCALL UnInitialize(void);
TXC_API bool  TXC_STDCALL SetCallback(TXC_CALLBACK callback);
TXC_API bool  TXC_STDCALL SetCallbackEx(TXC_CALLBACK_EX callback, void* user_data);
TXC_API BYTE* TXC_STDCALL SendCommand(BYTE* xml);
TXC_API bool  TXC_STDCALL FreeMemory(BYTE* memory);
TXC_API BYTE* TXC_STDCALL SetLogLevel(int level);
TXC_API int   TXC_STDCALL GetServiceInfo(const BYTE* request, BYTE** response);

#ifdef __cplusplus
}
#endif

#endif /* TXMLCONNECTOR_H */
