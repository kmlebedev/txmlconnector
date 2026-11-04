"""TCP sink that records what a TRANSAQ client sends, without a server.

usage: python3 sink.py PORT OUTFILE [REPLY_HEX_FILE]
Logs every received chunk per connection (seconds, length, hex). With a reply
file, sends its raw bytes (hex, whitespace ignored) after the first chunk.
Used to capture the first RQ_LOGIN of the DLL and to probe how it reacts to
arbitrary replies (its *_ts.log at TC_DLL_LOG_LEVEL=3 shows the parse error).
"""
import socket, sys, time, binascii, threading

port, out = int(sys.argv[1]), sys.argv[2]
reply = None
if len(sys.argv) > 3:
    reply = binascii.unhexlify("".join(open(sys.argv[3]).read().split()))

log = open(out, "a", buffering=1)
t0 = time.time()

def handle(conn, addr, n):
    log.write(f"# conn {n} from {addr} at {time.time()-t0:.3f}\n")
    sent = False
    conn.settimeout(60)
    try:
        while True:
            d = conn.recv(65536)
            if not d:
                log.write(f"# conn {n} EOF at {time.time()-t0:.3f}\n")
                break
            log.write(f"{n} {time.time()-t0:.3f} recv {len(d)} {d.hex()}\n")
            if reply is not None and not sent:
                conn.sendall(reply)
                sent = True
                log.write(f"{n} {time.time()-t0:.3f} sent {len(reply)} {reply.hex()}\n")
    except Exception as e:
        log.write(f"# conn {n} error {e!r}\n")
    finally:
        conn.close()

s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", port))
s.listen(5)
print("listening", port, flush=True)
n = 0
while True:
    c, a = s.accept()
    n += 1
    threading.Thread(target=handle, args=(c, a, n), daemon=True).start()
