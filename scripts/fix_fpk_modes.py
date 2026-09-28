"""Restore Unix executable bits lost when fnpack runs on Windows."""
import copy
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import tarfile
import sys


def rewrite(source: tarfile.TarFile, output: tarfile.TarFile, nested: bool) -> None:
    app_digest = None
    for member in source:
        item = copy.copy(member)
        if item.isdir():
            item.mode = 0o755
            output.addfile(item)
            continue
        if not item.isfile():
            output.addfile(item)
            continue
        data = source.extractfile(member).read()
        if not nested and item.name == "app.tgz":
            inner = io.BytesIO()
            with gzip.GzipFile(fileobj=inner, mode="wb", filename="", mtime=0) as compressed:
                with tarfile.open(fileobj=compressed, mode="w", format=tarfile.GNU_FORMAT) as archive:
                    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as original:
                        rewrite(original, archive, True)
            data = inner.getvalue()
            app_digest = hashlib.md5(data).hexdigest().encode("ascii")
        if not nested and item.name == "manifest":
            if app_digest is None:
                raise ValueError("app.tgz must precede manifest")
            data, count = re.subn(rb"(?m)^(checksum[ \t]*=[ \t]*)[0-9a-f]{32}", lambda m: m.group(1) + app_digest, data)
            if count != 1:
                raise ValueError("manifest checksum not found")
        item.mode = 0o755 if (nested and item.name in {"bin/fnvpn", "bin/mihomo"}) or (not nested and item.name.startswith("cmd/")) else 0o644
        item.size = len(data)
        output.addfile(item, io.BytesIO(data))


for name in sys.argv[1:]:
    path = Path(name)
    temporary = path.with_name(path.name + ".fixed")
    with tarfile.open(path, "r:gz") as original, open(temporary, "wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.GNU_FORMAT) as output:
                rewrite(original, output, False)
    os.replace(temporary, path)
