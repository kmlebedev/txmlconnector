"""Analyze RQ_MAIN replies of a decoded session.

usage: python3 analyze_main.py DUMP [--secnew-json OUT] [--dll CALLBACKS]
                               [--correlate XMLTAG FIELD ...]

DUMP: `tools/wire-decode -payload -1` output.

Always prints record tag statistics over all server RQ_MAIN payloads and the
tags where splitting stopped (records without size whose layout is unknown —
the next thing to reverse, see the tag map in the plan document).

--secnew-json  decodes every R_SEC_NEW (0x28) with the layout of
               FUN_180398640 (native/xmlcompat/security.go) to JSON.
--dll          harness output of the same session; with --correlate prints,
               for each XML element, a table of (record field value, XML value)
               pairs over all securities. A 1:1 table is a mapping rule; a
               table that splits shows a missing input. Example:
                 --correlate instrclass=Class sectype=Kind,Market
"""
import argparse
import json
import xml.etree.ElementTree as ET
from collections import Counter

from txproto import RQ, Reader, read_dump, split_records


def parse_secnew(body):
    r = Reader(body)
    d = dict(SecID=r.W(), Market=r.B(), Kind=r.B(), Class=r.B(), SecCode=r.Z(0x19), ShortName=r.Z(0x1e),
             ISIN=r.Z(0xc), Byte1=r.B(), Int1=r.L(), Flags=r.B(), Int2=r.L())
    if d["Kind"] in (4, 5):
        d["DerivFlags"] = r.B()
        if d["DerivFlags"] & 0xc:
            d["Underlying"] = r.Z(0x19)
    d["Props"] = r.B()
    pits = []
    while True:
        board = r.W()
        if board == 0xffff:
            break
        pits.append(dict(Board=board, LotSize=r.L(), LotDivider=r.L(), MinStep=r.L(), Decimals=r.B(), Byte1=r.B(),
                         PointCost=r.D(), Text=r.Z(0xc), Default=r.B(), Int1=r.L(), Currency=r.Z(4)))
    d["Pits"] = pits
    d["Currency"] = r.Z(4)
    if r.left():
        d["Tail1"], d["Tail2"] = r.B(), r.B()
    if r.left():
        d["Tail3"], d["TimeZone"] = r.L(), r.raw(16).hex()
    if r.left():
        d["MIC"], d["Ticker"] = r.Z(5), r.Z(0x19)
    if r.left():
        d["Tail4"], d["Tail5"], d["Text1"], d["Text2"] = r.L(), r.L(), r.Z(0x19), r.Z(6)
    if r.left():
        d["Tail6"] = r.B()
    d["Leftover"] = r.left()
    return d


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("dump")
    ap.add_argument("--secnew-json")
    ap.add_argument("--dll")
    ap.add_argument("--correlate", nargs="*", default=[])
    a = ap.parse_args()

    tags, stops, secs = Counter(), Counter(), []
    for f in read_dump(a.dump):
        if f["dir"] != "S>C" or f["type"] != RQ["MAIN"] or not f["payload"]:
            continue
        for tag, body in split_records(f["payload"]):
            if tag == "?":
                stops["%#04x" % body[0]] += 1
                continue
            tags["%#04x" % tag] += 1
            if tag == 0x28:
                secs.append(parse_secnew(body))
    print("records:", dict(sorted(tags.items())))
    print("stopped at unknown tags:", dict(stops))
    print("R_SEC_NEW: %d, leftover bytes: %s" % (len(secs), Counter(s["Leftover"] for s in secs)))
    if a.secnew_json:
        json.dump(secs, open(a.secnew_json, "w"), ensure_ascii=False)
    if a.dll and a.correlate:
        by_id = {s["SecID"]: s for s in secs}
        xml = {}
        for line in open(a.dll, encoding="utf-8"):
            if "CALLBACK: <securities>" in line:
                for sec in ET.fromstring(line.split("CALLBACK: ", 1)[1]):
                    xml[int(sec.get("secid"))] = sec
        for spec in a.correlate:
            tag, fields = spec.split("=")
            table = Counter()
            for sid, sec in xml.items():
                rec = by_id.get(sid)
                if rec is None:
                    continue
                key = tuple(rec.get(f) for f in fields.split(","))
                value = sec.get(tag) if tag in ("active",) else sec.findtext(tag)
                table[key + (value,)] += 1
            print("== %s vs %s (%d distinct)" % (tag, fields, len(table)))
            for row, n in table.most_common(15):
                print("  %6d  %s" % (n, row))


if __name__ == "__main__":
    main()
