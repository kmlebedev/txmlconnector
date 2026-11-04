"""Build lookup maps from Ghidra decompiler output (XrefDecomp.java).

usage:
  python3 dll_maps.py commands DECOMP_C   # C of FUN_1801774b0 (command table)
  python3 dll_maps.py tags DECOMP_C       # C of FUN_18038d7d0 (RQ_MAIN dispatcher)

commands: XML command id -> handler, and whether the DLL requires a
  connection (dispatcher FUN_1800a5030 answers "Cannot process this command
  without connection." otherwise). The handler is assigned in the block
  before the name, so it is tracked across lines.
tags: RQ_MAIN record tag -> name (from log strings in the case) -> first
  handler call; "*" marks records with a BE16 size (FUN_1801bdf00 first).
  The switch value in the dispatcher is tag-1.

The request type -> name -> builder map comes from CallArgs.java run on
FUN_180355d50 (see tools/re/README.md).
"""
import re
import sys


def commands(src):
    handler, name, out = None, None, []
    for line in src.split("\n"):
        m = re.search(r"local_\w+ = (FUN_18019[0-9a-f]+);", line)
        if m:
            handler = m.group(1)
        m = re.search(r'FUN_180008840\(\w+,"([a-z_0-9]+)"\)', line)
        if m:
            name = m.group(1)
        m = re.search(r"FUN_1800a4de0\(\w+,\w+,\w+,(\d)\)", line)
        if m:
            out.append((name, handler, m.group(1) == "1"))
    return out


def tags(src):
    body = src[src.index("switch(local_18c0)"):]
    labels = re.findall(r"\n      case (0x[0-9a-f]+|\d+):\n|\n      (default):\n", body)
    codes = re.split(r"\n      case (?:0x[0-9a-f]+|\d+):\n|\n      default:\n", body)[1:]
    pending, out = [], []
    for (lab, d), code in zip(labels, codes):
        pending.append(lab or d)
        if code.strip() == "":
            continue
        tag_list = ["%#04x" % (int(l, 0) + 1) for l in pending if l != "default"]
        names = re.findall(r'"(R_[A-Z_0-9]+|RQ_MAIN [A-Z_0-9 ]+|[A-Z_]{4,}):? ', code)
        calls = [c for c in re.findall(r"(FUN_18[0-9a-f]+)\(param_1", code) if c not in ("FUN_1803fdc30", "FUN_1801b5980")]
        sized = "FUN_1801bdf00" in code.strip().split("\n")[0]
        out.append(("/".join(tag_list) or "default", sized,
                     (names[0] if names else "-").replace("RQ_MAIN ", ""), calls[0] if calls else "-"))
        pending = []
    return out


def main():
    kind, path = sys.argv[1], sys.argv[2]
    src = open(path).read()
    if kind == "commands":
        for name, handler, needs in commands(src):
            print("%-26s %s %s" % (name, handler, "needs-connection" if needs else "no-connection"))
    elif kind == "tags":
        for tag, sized, name, handler in tags(src):
            print("%-10s %s %-22s %s" % (tag, "*" if sized else " ", name, handler))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
