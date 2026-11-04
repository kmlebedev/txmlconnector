"""TRANSAQ native protocol helpers for research scripts (built-in crypto profile).

The Go implementation in native/ is the reference; this module mirrors it so
stubs, replays and analyzers can be written quickly in Python. Vendor tables
(S-boxes, default key and sync) are read from native/cryptoprofile/tables.go,
so the DLL is not needed for crypto. See tools/re/README.md.

Wire format summary (docs/protocol/connect.md):
  header 8 bytes: flags, type, len BE16, crc16(payload) BE16, crc16(hdr[0:6]) BE16
  header and payload are encrypted separately, each from the start of the gamma;
  client->server: GOST counter mode (gamma); server->client: GOST CFB;
  flags & 0x80 on a reply: payload sent in clear.
"""
import hashlib
import os
import re
import struct

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
TABLES_GO = os.path.join(REPO, "native", "cryptoprofile", "tables.go")

KIND_REQUEST, KIND_REPLY, KIND_BYE = 1, 2, 3
FLAG_PLAIN = 0x80

# Request types (call sites of FUN_180355d50; full map in the plan document).
RQ = {"LOGIN": 1, "LOGOUT": 2, "MAIN": 5, "STARTUP": 9, "SUBSCRIBE": 10, "SEC_SPEC": 15,
      "CLIENT_HOLDINGS": 42, "PWD": 46, "TIME": 50, "RELOGIN": 63, "RECOVER": 64}


def _load_tables(path=TABLES_GO):
    src = open(path).read()
    sbox = "".join(re.findall(r'"([0-9a-f]{64})"', src.split("var sboxTable")[1].split(")")[0]))
    key = re.search(r'DefaultKey\s*=\s*mustHex\("([0-9a-f]+)"\)', src).group(1)
    sync = re.search(r'DefaultSync\s*=\s*\[8\]byte\(mustHex\("([0-9a-f]+)"\)\)', src).group(1)
    return bytes.fromhex(sbox), bytes.fromhex(key), bytes.fromhex(sync)


SBOX, DEFAULT_KEY, DEFAULT_SYNC = _load_tables()
assert len(SBOX) == 0x400 and len(DEFAULT_KEY) == 32 and len(DEFAULT_SYNC) == 8

_ORDER = list(range(8)) * 3 + list(range(7, -1, -1))


class Gost:
    """GOST 28147-89 with the vendor 8-bit S-box tables."""

    def __init__(self, key):
        assert len(key) == 32
        self.k = struct.unpack("<8I", key)

    def _f(self, x):
        y = (SBOX[0x300 + (x >> 24)] << 24 | SBOX[0x200 + (x >> 16 & 0xff)] << 16 |
             SBOX[0x100 + (x >> 8 & 0xff)] << 8 | SBOX[x & 0xff])
        return ((y << 11) | (y >> 21)) & 0xffffffff

    def block(self, n1, n2):
        for i in _ORDER:
            n1, n2 = n2 ^ self._f((n1 + self.k[i]) & 0xffffffff), n1
        return n2, n1

    def gamma(self, data, sync):
        """Counter mode (FUN_18043a520); its own inverse."""
        n1, n2 = self.block(*struct.unpack("<II", sync))
        out = bytearray()
        for off in range(0, len(data), 8):
            n2 = (n2 + 0x01010104 + (1 if n2 > 0xfefefefb else 0)) & 0xffffffff
            n1 = (n1 + 0x01010101) & 0xffffffff
            g = struct.pack("<II", *self.block(n1, n2))
            out += bytes(a ^ b for a, b in zip(data[off:off + 8], g))
        return bytes(out)

    def cfb(self, data, sync, decrypt=False):
        """CFB (FUN_18043a5d0 encrypt, FUN_18043a660 decrypt)."""
        n1, n2 = struct.unpack("<II", sync)
        out = bytearray()
        for off in range(0, len(data), 8):
            g = struct.pack("<II", *self.block(n1, n2))
            src = data[off:off + 8]
            blk = bytes(a ^ b for a, b in zip(src, g))
            out += blk
            n1, n2 = struct.unpack("<II", (src if decrypt else blk) + g[len(src):])
        return bytes(out)


def crc16(b):
    """CRC-16/CCITT-FALSE (FUN_18043d7c0)."""
    c = 0xffff
    for x in b:
        c ^= x << 8
        for _ in range(8):
            c = ((c << 1) ^ 0x1021) & 0xffff if c & 0x8000 else (c << 1) & 0xffff
    return c


def password_key(password):
    """SHA-1 of the password expanded to 32 bytes (FUN_18043d370)."""
    out = bytearray(hashlib.sha1(password.encode("cp1251")).digest())
    for i in range(20, 32):
        c = (out[i - 20] - i + 32) & 0xff
        out.append((((c >> 5) | (c << 3)) & 0xff) ^ 0x97)
    return bytes(out)


class Keys:
    """Key material of one direction pair; the built-in profile uses one key
    and sync for both directions."""

    def __init__(self, key=DEFAULT_KEY, sync=DEFAULT_SYNC):
        self.cipher, self.sync = Gost(key), sync

    def _crypt(self, data, client_to_server, encrypt):
        if client_to_server:
            return self.cipher.gamma(data, self.sync)
        return self.cipher.cfb(data, self.sync, decrypt=not encrypt)

    def encode(self, flags, typ, payload, client_to_server):
        if client_to_server:
            flags &= 0x7f  # the client never sends plain payloads (FUN_1801bfb20)
        hdr = bytes([flags, typ]) + struct.pack(">HH", len(payload), crc16(payload))
        hdr += struct.pack(">H", crc16(hdr))
        body = payload if flags & FLAG_PLAIN or not payload else self._crypt(payload, client_to_server, True)
        return self._crypt(hdr, client_to_server, True) + body

    def decode(self, recv, client_to_server):
        """recv(n) -> exactly n bytes. Returns (flags, type, payload); raises on CRC errors."""
        hdr = self._crypt(recv(8), client_to_server, False)
        if struct.unpack(">H", hdr[6:8])[0] != crc16(hdr[:6]):
            raise ValueError("header crc")
        n = struct.unpack(">H", hdr[2:4])[0]
        body = recv(n) if n else b""
        if n and not hdr[0] & FLAG_PLAIN:
            body = self._crypt(body, client_to_server, False)
        if struct.unpack(">H", hdr[4:6])[0] != crc16(body):
            raise ValueError("payload crc")
        return hdr[0], hdr[1], body


def recv_exact(sock):
    def rx(n):
        b = b""
        while len(b) < n:
            x = sock.recv(n - len(b))
            if not x:
                raise EOFError
            b += x
        return b
    return rx


# ---- field codecs (native/wire/codec.go) ----

def W(v):
    return struct.pack(">H", v & 0xffff)


def Z(s, enc="cp1251"):
    return s.encode(enc) + b"\0"


def packed(v, wide=False):
    """Packed signed integer: L (FUN_1801be7d0) or, with wide, G (count 7 = 8 bytes)."""
    neg = v < 0
    m = -v if neg else v
    head = (8 if neg else 0) | (m & 0xf) << 4
    m >>= 4
    ext = bytearray()
    while m:
        ext.append(m & 0xff)
        m >>= 8
    if len(ext) >= 7:
        if not wide:
            raise ValueError("value needs the wide form")
        ext = ext.ljust(8, b"\0")
        return bytes([head | 7]) + bytes(ext)
    return bytes([head | len(ext)]) + bytes(ext)


class Reader:
    def __init__(self, b, off=0, end=None):
        self.b, self.o, self.end = b, off, len(b) if end is None else end

    def left(self):
        return self.end - self.o

    def B(self):
        self.o += 1
        return self.b[self.o - 1]

    def W(self):
        self.o += 2
        return self.b[self.o - 2] << 8 | self.b[self.o - 1]

    def Z(self, mx):
        i = self.b.index(0, self.o)
        if i - self.o > mx:
            raise ValueError("string longer than %d" % mx)
        s = self.b[self.o:i].decode("cp1251")
        self.o = i + 1
        return s

    def raw(self, n):
        self.o += n
        return self.b[self.o - n:self.o]

    def D(self):
        return struct.unpack("<d", self.raw(8))[0]

    def L(self, wide=False):
        h = self.b[self.o]
        n = h & 7
        if wide and n == 7:
            n = 8
        m = 0
        for i in range(n, 0, -1):
            m = m << 8 | self.b[self.o + i]
        m = m << 4 | h >> 4
        self.o += n + 1
        return -m if h & 8 else m

    def G(self):
        return self.L(wide=True)


# Records of RQ_MAIN that carry a BE16 size after the tag (FUN_1801bdf00).
SIZED_TAGS = {0x02, 0x03, 0x04, 0x08, 0x10, 0x28, 0x3a, 0x3b, 0x54, 0x5e, 0x5f, 0x66}
WRAPPER = 0x5f


def split_records(payload):
    """Yield (tag, body) for an RQ_MAIN payload; stops with ('?', rest) at an
    unknown unsized tag. Mirrors native/records.Records."""
    r = Reader(payload)
    while r.left() > 0:
        start = r.o
        tag = r.B()
        if tag in SIZED_TAGS:
            n = r.W()
            if tag == WRAPPER:
                continue
            yield tag, r.raw(n)
        elif tag == 0x3d:        # R_DATE
            r.L()
            yield tag, payload[start + 1:r.o]
        elif tag == 0x0a:        # R_SEC_RATE: W secid, L
            r.W()
            r.L()
            yield tag, payload[start + 1:r.o]
        elif tag in (0x0b, 0x0c, 0x0d, 0x0e):
            yield tag, r.raw(3)
        else:
            yield "?", payload[start:]
            return


def read_dump(path):
    """Read `tools/wire-decode -payload -1` output: list of dicts in wire order."""
    frames = []
    for line in open(path):
        cols = line.split()
        if len(cols) < 7 or cols[0].startswith("#"):
            continue
        frames.append({
            "t": float(cols[0]), "conn": cols[1], "dir": cols[2],
            "flags": int(cols[4].split("=")[1], 16), "type": int(cols[5].split("=")[1]),
            "payload": bytes.fromhex(cols[7]) if len(cols) > 7 and not cols[7].endswith("…") else b"",
        })
    return frames
