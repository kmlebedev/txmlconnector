package dlllog

// consoleEOL is the stdout line end: the DLL writes text-mode "\n", which the
// Windows CRT turns into CRLF (O under Wine).
const consoleEOL = "\r\n"
