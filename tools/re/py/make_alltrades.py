"""Build R_ALLTRADE (0x08) records for the oracle (replay_server.py --inject).

usage: python3 make_alltrades.py SET OUT_HEX     SET = 1, 2 or 3

Sets 1-3 are the records behind native/xmlcompat/testdata/alltrades-inject*.hex;
the DLL's <alltrades> answers are alltrades-dll*.xml. They use secids of the
recorded demo session (SBER 26, GAZP 379, LKOH 4, VTBR 889; TQBR = board 37).
Copy a set and change one field at a time to learn what it means.

Layout (FUN_1803bf700, docs/protocol/alltrades.md):
  W secid, W board, G tradeno, L time, L price, L quantity, W flags, then by flag:
  0x01 B period, 0x02 L, 0x04 G, 0x02 G, 0x08 B buysell, 0x10 Z12,
  0x20 L L G G, 0x40 L openinterest, 0x80 Z4, 0x100 W milliseconds
"""
import sys

from txproto import W, Z, packed


def alltrade(secid, board, tradeno, t, price, qty, flags, extra=b"", wide_tradeno=False):
    body = (W(secid) + W(board) + packed(tradeno, wide=True if wide_tradeno else False) +
            packed(t) + packed(price) + packed(qty) + W(flags) + extra)
    return bytes([0x08]) + W(len(body)) + body


T = 1793750000  # 2026-11-03 23:53:20 on the server wall clock (MSK)

SETS = {
    "1": [
        alltrade(0x1a, 37, 9000000001, T, 30512, 3, 0x01, b"B"),
        alltrade(0x1a, 37, 9000000002, T + 1, 30513, 10, 0x01 | 0x02 | 0x08, b"S" + packed(1234) + packed(98765) + b"N"),
        alltrade(0x17b, 37, 9000000003, T + 2, 12345, 7, 0x04 | 0x10 | 0x40 | 0x80 | 0x100,
                 packed(555555) + Z("REF12") + packed(777) + Z("RUB") + W(250)),
        alltrade(0x4, 37, 9000000004, T + 3, 654321, 1, 0x20, packed(11) + packed(22) + packed(33) + packed(44)),
        alltrade(0x379, 37, 9000000005, 0, 98, 1000, 0x00),
    ],
    "2": [
        alltrade(0x1a, 37, 9100000001, T, 30512, 3, 0x08, b"B"),
        alltrade(0x1a, 37, 9100000002, T, 30512, 4, 0x08, b"S"),
        alltrade(0x1a, 37, 9100000003, T, 30512, 5, 0x01 | 0x08, b"O" + b"B"),
        alltrade(0x1a, 37, 9100000004, T, 30512, 6, 0x01, b"C"),
        alltrade(0x1a, 37, 9100000005, T, -30512, 7, 0x00),
        alltrade(0x1a, 37, 9100000006, T, 30512, 8, 0x02, packed(5) + packed(6)),
    ],
    "3": [
        alltrade(0x1a, 37, 9200000001, T, 30500, 1, 0x00),
        alltrade(0x1a, 37, 9200000002, T, 30510, 1, 0x00),
        alltrade(0x4, 37, 9200000003, T, 654320, 1, 0x00),
        alltrade(0x1a, 37, (1 << 62) + 12345, T, 30512, 1, 0x00, wide_tradeno=True),
        alltrade(0x1a, 37, 9200000005, T, 30512, 1, 0x40 | 0x100, packed(-5) + W(7)),
    ],
}

if __name__ == "__main__":
    open(sys.argv[2], "w").write(b"".join(SETS[sys.argv[1]]).hex())
