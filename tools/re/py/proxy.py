"""Logging TCP forwarder between a TRANSAQ client (DLL or native) and a server.

usage: python3 proxy.py LPORT HOST PORT OUTFILE
Point the client at 127.0.0.1:LPORT. Each received chunk is appended to
OUTFILE as "<conn> <seconds> C>S|S>C <len> <hex>", the format read by
tools/wire-decode (decrypts frames) and replay_server.py.
Captures of real sessions contain account data: keep them out of git.
"""
import socket, sys, threading, time

lport, host, port, out = int(sys.argv[1]), sys.argv[2], int(sys.argv[3]), sys.argv[4]
log = open(out, "a", buffering=1)
lock = threading.Lock()
t0 = time.time()

def w(line):
    with lock:
        log.write(line + "\n")

def pump(n, src, dst, tag):
    try:
        while True:
            d = src.recv(65536)
            if not d:
                break
            w(f"{n} {time.time()-t0:.3f} {tag} {len(d)} {d.hex()}")
            dst.sendall(d)
    except OSError as e:
        w(f"# {n} {tag} {e!r}")
    finally:
        try: dst.shutdown(socket.SHUT_WR)
        except OSError: pass

s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", lport)); s.listen(5)
print("listening", flush=True)
n = 0
while True:
    c, _ = s.accept(); n += 1
    u = socket.create_connection((host, port), timeout=15); u.settimeout(None)
    w(f"# conn {n} at {time.time()-t0:.3f}")
    threading.Thread(target=pump, args=(n, c, u, "C>S"), daemon=True).start()
    threading.Thread(target=pump, args=(n, u, c, "S>C"), daemon=True).start()
