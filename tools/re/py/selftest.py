"""Check txproto.py against byte vectors captured from the DLL.

usage: python3 tools/re/py/selftest.py
The vectors are the same as in native/wire and native/session tests.
"""
import io

from txproto import (DEFAULT_SYNC, Gost, Keys, Reader, crc16, packed, password_key)

LOGIN_PAYLOAD = "78004e0001021c544553544c4f47494e00ffe8"   # RQ_LOGIN for TESTLOGIN
LOGIN_WIRE = "e920e526b0d5603b9021ab352b9268fca36d964c861698e8f82e56"
STUB_LOGIN_REPLY_WIRE = "d7c6c64985ed4716d5cfc74138835a3ee034"


def main():
    assert crc16(b"123456789") == 0x29B1
    k = Keys()
    assert k.encode(1, 1, bytes.fromhex(LOGIN_PAYLOAD), True).hex() == LOGIN_WIRE
    raw = io.BytesIO(bytes.fromhex(LOGIN_WIRE))
    assert k.decode(raw.read, True)[2].hex() == LOGIN_PAYLOAD
    raw = io.BytesIO(bytes.fromhex(STUB_LOGIN_REPLY_WIRE))
    flags, typ, p = k.decode(raw.read, False)
    assert (flags, typ, p.hex()) == (2, 1, "00080102030405060708")
    assert k.encode(2, 1, p, False).hex() == STUB_LOGIN_REPLY_WIRE
    proof = Gost(password_key("TESTPASS")).gamma(bytes(range(1, 9)), DEFAULT_SYNC)
    assert proof.hex() == "211cf3ef536a73bf"
    assert packed(16782022).hex() == "632c0110" and packed(-2130466624).hex() == "0c74c5ef07"
    assert Reader(packed((1 << 62) + 12345, wide=True)).G() == (1 << 62) + 12345
    print("txproto selftest: OK")


if __name__ == "__main__":
    main()
