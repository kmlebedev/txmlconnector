"""Scripted TRANSAQ server for probing the DLL (or the native client) offline.

usage: python3 stub_server.py PORT [--password P] [--uid N] [--sid N]
                              [--reply TYPE:HEX ...] [--log FILE]

Speaks the server side of the built-in profile: answers RQ_LOGIN with an
8-byte challenge, RQ_PWD with a successful reply that issues a fixed session
key (bytes 0x40..0x5f, sync 0x70..0x77), then switches keys like the DLL.
Every later request is answered from --reply (first unused entry of that
type, payload hex, sent plain with flags 0x82) or with an empty reply.
Each decoded client frame is printed, which is how request layouts are read
(RQ_TIME, RQ_STARTUP, RQ_SUBSCRIBE ...).

Typical use (step "RE-2 without a server" in tools/re/README.md):
  python3 stub_server.py 39010 --password TESTPASS \\
      --reply 50:74c0a4ae064cff0000c4ff00000000000000000000
then connect the DLL harness to 127.0.0.1:39010 with login/password of the
test account and watch the DLL log (TC_DLL_LOG_LEVEL=3).
"""
import argparse
import socket
import sys

from txproto import (DEFAULT_SYNC, KIND_BYE, Gost, Keys, RQ, packed, password_key, recv_exact)

SESSION_KEY = bytes(range(0x40, 0x60))
SESSION_SYNC = bytes(range(0x70, 0x78))


def pwd_reply(password, uid=4242, sid=7, sys_ver=626, build=4, session_timeout=120):
    """RQ_PWD success payload (field order: native/session parsePwdReply)."""
    k = Gost(password_key(password))
    p = b"\x00" + k.cfb(SESSION_KEY, DEFAULT_SYNC) + k.cfb(SESSION_SYNC, DEFAULT_SYNC)
    p += b"\x00\x00\x00" + packed(session_timeout) + b"\x00\x00\x00\x00" + packed(0)
    # Flags, Byte8, SubBroker, Text1, Byte9, Text2, Text3, Byte10
    p += b"\x00" * 8
    p += packed(uid) + b"\x00" + bytes([sid]) + b"\x00\x00" + packed(sys_ver) + packed(build)
    return p


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("port", type=int)
    ap.add_argument("--password", default="TESTPASS")
    ap.add_argument("--uid", type=int, default=4242)
    ap.add_argument("--sid", type=int, default=7)
    ap.add_argument("--challenge", default="0102030405060708")
    ap.add_argument("--reply", action="append", default=[], help="TYPE:HEX, repeatable")
    ap.add_argument("--log", default=None)
    a = ap.parse_args()
    out = open(a.log, "a", buffering=1) if a.log else sys.stdout
    queued = {}
    for item in a.reply:
        t, h = item.split(":", 1)
        queued.setdefault(int(t, 0), []).append(bytes.fromhex(h))

    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("127.0.0.1", a.port))
    s.listen(1)
    print("listening on", a.port, file=out)
    c, _ = s.accept()
    rx = recv_exact(c)
    keys = Keys()
    try:
        while True:
            flags, typ, p = keys.decode(rx, True)
            print("C>S type=%d flags=%#x len=%d %s" % (typ, flags, len(p), p.hex()), file=out)
            if typ == RQ["LOGIN"]:
                ch = bytes.fromhex(a.challenge)
                c.sendall(keys.encode(2, typ, b"\x00" + bytes([len(ch)]) + ch, False))
            elif typ == RQ["PWD"]:
                c.sendall(keys.encode(2, typ, pwd_reply(a.password, a.uid, a.sid), False))
                keys = Keys(SESSION_KEY, SESSION_SYNC)
                print("session key switched", file=out)
            elif typ == RQ["LOGOUT"]:
                print("logout", file=out)
                break
            else:
                body = queued[typ].pop(0) if queued.get(typ) else b""
                c.sendall(keys.encode(0x82, typ, body, False))
    except (EOFError, ConnectionResetError):
        print("connection closed", file=out)
    except ValueError as e:  # CRC error: wrong keys or wrong password
        print("decode error:", e, file=out)
        c.sendall(keys.encode(KIND_BYE, 0, b"", False))


if __name__ == "__main__":
    main()
