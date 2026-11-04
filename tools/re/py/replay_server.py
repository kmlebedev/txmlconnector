"""Replay a recorded TRANSAQ session to a client and inject crafted records.

usage: python3 replay_server.py DUMP PORT [--inject HEX_FILE] [--after TYPE]
       (password of the recorded account in TC_PASSWORD)

DUMP is `tools/wire-decode -payload -1 capture.log` of a real session (DLL
or native client). The server answers every request with the next recorded
reply of the same type. RQ_MAIN replies are replayed only up to the first
request of type --after (default 10, RQ_SUBSCRIBE), so the client gets the
real login, dictionaries and snapshot. The first RQ_MAIN after that request
returns the payload from --inject (hex of RQ_MAIN records, see
make_alltrades.py); later RQ_MAIN replies are empty.

The session key comes from the recorded RQ_PWD reply, decrypted with the
account password, so the replay works for any client that logs in with the
same password. The challenge proof is not checked.

This is procedure RE-3 ("oracle"): run the DLL harness against the replay and
read the XML the DLL emits for the injected records; that fixes what each
field means. See tools/re/README.md.
"""
import argparse
import os
import socket
import sys

from txproto import (DEFAULT_SYNC, Gost, Keys, RQ, password_key, read_dump, recv_exact)


def load(dump, after):
    replies, cut = {}, None
    for f in read_dump(dump):
        if f["dir"] == "C>S" and f["type"] == after and cut is None:
            cut = f["t"]
        if f["dir"] != "S>C":
            continue
        if f["type"] == RQ["MAIN"] and cut is not None:
            continue
        replies.setdefault(f["type"], []).append((f["flags"], f["payload"]))
    return replies


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("dump")
    ap.add_argument("port", type=int)
    ap.add_argument("--inject", default=None)
    ap.add_argument("--after", type=int, default=RQ["SUBSCRIBE"])
    a = ap.parse_args()
    password = os.environ.get("TC_PASSWORD")
    if not password:
        sys.exit("TC_PASSWORD is required to unwrap the recorded session key")
    replies = load(a.dump, a.after)
    print({k: len(v) for k, v in sorted(replies.items())}, flush=True)
    pwd = replies[RQ["PWD"]][0][1]
    pk = Gost(password_key(password))
    session = Keys(pk.cfb(pwd[1:33], DEFAULT_SYNC, decrypt=True), pk.cfb(pwd[33:41], DEFAULT_SYNC, decrypt=True))
    inject = bytes.fromhex(open(a.inject).read().strip()) if a.inject else b""

    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("127.0.0.1", a.port))
    s.listen(1)
    print("listening on", a.port, flush=True)
    c, _ = s.accept()
    rx = recv_exact(c)
    keys = Keys()
    armed = injected = False
    try:
        while True:
            _, typ, p = keys.decode(rx, True)
            if typ == RQ["LOGOUT"]:  # sent without waiting for a reply
                break
            if typ == a.after:
                armed = True
                print("trigger request type %d: %s" % (typ, p.hex()), flush=True)
            if typ == RQ["MAIN"] and armed:
                flags, body = (0x82, b"") if injected else (0x82, inject)
                injected = True
            elif replies.get(typ):
                flags, body = replies[typ].pop(0)
            else:
                flags, body = 0x82, b""
            c.sendall(keys.encode(flags, typ, body, False))
            if typ == RQ["PWD"]:
                keys = session
    except (EOFError, ConnectionResetError):
        pass
    print("done, injected=%s" % injected, flush=True)


if __name__ == "__main__":
    main()
