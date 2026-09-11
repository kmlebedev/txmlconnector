"""Read-only PE inventory; standard library only. Does not load the DLL.

Usage: python3 docs/research/inspect_binary.py DLL_PATH OUTPUT_JSON
"""
import hashlib
import json
import pathlib
import re
import struct
import sys


def inspect(path):
    data = path.read_bytes()
    pe = struct.unpack_from('<I', data, 0x3C)[0]
    if data[:2] != b'MZ' or data[pe:pe + 4] != b'PE\0\0':
        raise ValueError('Not a PE file')
    machine, count, timestamp = struct.unpack_from('<HHI', data, pe + 4)
    optional_size = struct.unpack_from('<H', data, pe + 20)[0]
    optional = pe + 24
    if struct.unpack_from('<H', data, optional)[0] != 0x20B:
        raise ValueError('Expected PE32+')
    image_base = struct.unpack_from('<Q', data, optional + 24)[0]
    sections = []
    for i in range(count):
        offset = optional + optional_size + 40 * i
        name = data[offset:offset + 8].rstrip(b'\0').decode('ascii')
        virtual_size, rva, raw_size, raw_offset = struct.unpack_from('<IIII', data, offset + 8)
        sections.append(dict(name=name, rva=rva, virtual_size=virtual_size,
                             raw_offset=raw_offset, raw_size=raw_size))

    def file_offset(rva):
        for section in sections:
            delta = rva - section['rva']
            if 0 <= delta < section['raw_size']:
                return section['raw_offset'] + delta
        raise ValueError(f'Unmapped RVA {rva:x}')

    def string_at(offset):
        return data[offset:data.index(b'\0', offset)].decode('ascii', errors='replace')

    exports = []
    export_rva = struct.unpack_from('<I', data, optional + 112)[0]
    if export_rva:
        directory = file_offset(export_rva)
        base, functions, names, functions_rva, names_rva, ordinals_rva = struct.unpack_from('<IIIIII', data, directory + 16)
        for i in range(names):
            name_rva = struct.unpack_from('<I', data, file_offset(names_rva) + i * 4)[0]
            ordinal = struct.unpack_from('<H', data, file_offset(ordinals_rva) + i * 2)[0]
            target_rva = struct.unpack_from('<I', data, file_offset(functions_rva) + ordinal * 4)[0]
            exports.append(dict(name=string_at(file_offset(name_rva)), ordinal=base + ordinal, rva=hex(target_rva)))

    pattern = re.compile(r'RQ_[A-Z_]+|HANDSHAKE:|CTSClient|ReadHistory|Cryptosync|Cryptokey|inflate 1\.2\.5|cT(?:My)?Crypto|AesCtrCipher|GostCipher|CSocketStream|\.pdb$|sslpro\.dll|mespro\.dll|Keccak-1600')
    anchors = []
    for match in re.finditer(rb'[\x20-\x7e]{6,}', data):
        value = match.group().decode('ascii')
        if len(value) > 350 or not pattern.search(value):
            continue
        offset = match.start()
        rva = None
        for section in sections:
            delta = offset - section['raw_offset']
            if 0 <= delta < section['raw_size']:
                rva = section['rva'] + delta
                break
        anchors.append(dict(file_offset=hex(offset), rva=hex(rva) if rva is not None else None, text=value))
    return dict(filename=path.name, size=len(data), sha256=hashlib.sha256(data).hexdigest(),
                machine=hex(machine), image_base=hex(image_base), pe_timestamp=timestamp,
                sections=sections, exports=exports, anchors=anchors,
                caveat='Strings are static evidence, not proof of active network algorithms. RVA differs from file offset; runtime VA depends on ASLR.')


if __name__ == '__main__':
    source, destination = map(pathlib.Path, sys.argv[1:])
    result = inspect(source)
    destination.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(f"{result['filename']}: {result['sha256']}; {len(result['exports'])} exports; {len(result['anchors'])} anchors")
