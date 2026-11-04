"""Decode an RQ_STARTUP reply and compare it with the DLL's dictionaries.

usage: python3 analyze_startup.py STARTUP_HEX [DLL_CALLBACKS]

STARTUP_HEX: payload hex of the RQ_STARTUP reply (one line; e.g.
native/xmlcompat/testdata/startup-demo.hex, or taken from a wire-decode dump).
DLL_CALLBACKS: harness output with "CALLBACK: <markets>/<boards>/<candlekinds>"
lines from the same session; when given, the rebuilt XML is compared byte for
byte. This is how the six sections and the market 5 -> 4 rule were found
(docs/protocol/dictionaries.md). Prints each section and the consumed length.
"""
import sys
from xml.sax.saxutils import escape

from txproto import Reader


def sections(payload):
    r = Reader(payload)
    out = {}

    def section(name, idf, end, fields):
        rows = []
        while True:
            i = idf()
            if i == end:
                break
            rows.append([i] + [f() for f in fields])
        out[name] = rows

    section("markets", r.B, 0xff, [lambda: r.Z(10), lambda: r.Z(0x18), r.B, r.B, r.B, r.B])
    out["markets_tail"] = r.W()
    section("boardtypes", r.B, 0xff, [lambda: r.Z(0x20), lambda: r.Z(8), r.B])
    section("boards", r.W, 0xffff, [r.B, lambda: r.Z(7), lambda: r.Z(0x1e), r.W, r.B, lambda: r.Z(8), r.B, r.B])
    section("sectypes", r.B, 0xff, [lambda: r.Z(10), lambda: r.Z(0x18), r.B, r.B, r.B])
    section("candlekinds", r.B, 0xff, [lambda: r.Z(12), r.L, r.L])
    section("columns", r.B, 0xff, [r.B, lambda: r.Z(0x20), lambda: r.Z(0x10)])
    out["consumed"] = (r.o, len(payload))
    return out


def rebuilt_xml(s):
    markets = "<markets>" + "".join('<market id="%d">%s</market>' % (m[0], escape(m[1])) for m in sorted(s["markets"])) + "</markets>"
    boards = "<boards>" + "".join('<board id="%s"><name>%s</name><market>%d</market><type>%d</type></board>' % (
        escape(b[2]), escape(b[3]), 4 if b[1] == 5 else b[1], b[8]) for b in s["boards"]) + "</boards>"
    kinds = "<candlekinds>" + "".join("<kind><id>%d</id><period>%d</period><name>%s</name></kind>" % (
        k[0], k[2], escape(k[1])) for k in s["candlekinds"]) + "</candlekinds>"
    return {"markets": markets, "boards": boards, "candlekinds": kinds}


def main():
    payload = bytes.fromhex(open(sys.argv[1]).read().strip())
    s = sections(payload)
    for name in ("markets", "boardtypes", "boards", "sectypes", "candlekinds", "columns"):
        print("%-11s %3d rows, first %s" % (name, len(s[name]), s[name][:1]))
    print("consumed %d of %d bytes" % s["consumed"])
    if len(sys.argv) > 2:
        dll = {}
        for line in open(sys.argv[2], encoding="utf-8", errors="replace"):
            if "CALLBACK: <" in line:
                xml = line.split("CALLBACK: ", 1)[1].rstrip("\n")
                dll.setdefault(xml[1:].split(">")[0].split(" ")[0], xml)
        for name, xml in rebuilt_xml(s).items():
            print("%-11s matches DLL: %s" % (name, xml == dll.get(name)))


if __name__ == "__main__":
    main()
