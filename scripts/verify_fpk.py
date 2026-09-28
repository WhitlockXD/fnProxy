"""Check FPK structure, architecture, executable bits and bundled notices."""
import hashlib
import io
import json
from pathlib import Path
import re
import sys
import tarfile


for raw in sys.argv[1:]:
    path = Path(raw)
    assert path.read_bytes()[:3] == b"\x1f\x8b\x08", "FPK outer layer must be gzip"
    expected = "x86" if path.name.endswith("_x86.fpk") else "arm" if path.name.endswith("_arm.fpk") else None
    if expected is None:
        raise SystemExit(f"Unknown FPK target: {path}")
    machine = 62 if expected == "x86" else 183
    with tarfile.open(path, "r:gz") as outer:
        entries = {m.name: m for m in outer}
        for name in ("manifest", "LICENSE", "cmd/main", "cmd/uninstall_init", "app.tgz", "ICON.PNG", "ICON_256.PNG"):
            assert name in entries, name
        manifest = outer.extractfile("manifest").read().decode("utf-8")
        assert re.search(rf"(?m)^platform\s*=\s*{expected}\s*$", manifest)
        assert re.search(r"(?m)^version\s*=\s*0\.1\.10\s*$", manifest), "version mismatch"
        assert re.search(r"(?m)^display_name\s*=\s*fnProxy\s*$", manifest), "display name mismatch"
        app_checksum=hashlib.md5(outer.extractfile("app.tgz").read()).hexdigest()
        assert re.search(rf"(?m)^checksum\s*=\s*{app_checksum}\s*$", manifest), "app.tgz checksum mismatch"
        install_init=outer.extractfile("cmd/install_init").read().decode("utf-8")
        assert ("x86_64" if expected=="x86" else "aarch64") in install_init
        assert "gatewayPrefix" not in manifest
        for name, member in entries.items():
            if name.startswith("cmd/") and member.isfile():
                assert member.mode & 0o111, f"not executable: {name}"
        root_icons = {name: outer.extractfile(name).read() for name in ("ICON.PNG", "ICON_256.PNG")}
        data = outer.extractfile("app.tgz").read()
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as inner:
        files = {m.name: m for m in inner}
        for name in ("bin/fnvpn", "bin/mihomo", "ui/config", "ui/images/icon_64.png", "ui/images/icon_256.png", "ui/images/fnproxy_0110_64.png", "ui/images/fnproxy_0110_256.png", "www/index.html", "www/app.js", "www/style.css", "www/icon.png", "licenses/MIHOMO_LICENSE", "licenses/YAML_LICENSE", "rules/cn-domain.mrs", "rules/cn-ip.mrs", "licenses/RULES_LICENSE"):
            assert name in files, name
        ui = json.load(inner.extractfile("ui/config"))
        icon_pattern = ui[".url"]["fnvpn.main"]["icon"]
        assert icon_pattern == "images/fnproxy_0110_{0}.png", "desktop icon path mismatch"
        for size, root_name in ((64, "ICON.PNG"), (256, "ICON_256.PNG")):
            source = (Path(__file__).resolve().parents[1] / "packaging" / "icons" / f"icon_{size}.png").read_bytes()
            paths = (f"ui/images/icon_{size}.png", f"ui/{icon_pattern.format(size)}")
            for name in paths:
                assert inner.extractfile(name).read() == source, f"incorrect packaged icon: {name}"
            assert root_icons[root_name] == source, f"incorrect package icon: {root_name}"
            assert source[:8] == b"\x89PNG\r\n\x1a\n" and int.from_bytes(source[16:20], "big") == size and int.from_bytes(source[20:24], "big") == size
            if size == 64:
                assert inner.extractfile("www/icon.png").read() == source, "incorrect web icon"
        html = inner.extractfile("www/index.html").read().decode("utf-8")
        assert '<link rel="icon" type="image/png" href="/app/fnvpn/icon.png?v=0.1.10">' in html
        for name, expected in {
            "rules/cn-domain.mrs": "6c4f403acc88c339a9aa77ecd7f711bc2506699cf810681b868547fcd1a480a1",
            "rules/cn-ip.mrs": "4cc9ab3b7e2bbd18e0420e09af42818d0748a596dd3daaed470bb2d9958d118d",
        }.items():
            assert hashlib.sha256(inner.extractfile(name).read()).hexdigest() == expected, name
        for name in ("bin/fnvpn", "bin/mihomo"):
            assert files[name].mode & 0o111, f"not executable: {name}"
            binary = inner.extractfile(name).read(20)
            assert binary[:4] == b"\x7fELF" and int.from_bytes(binary[18:20], "little") == machine, f"wrong architecture: {name}"
        assert ui[".url"]["fnvpn.main"]["gatewayPrefix"] == "/app/fnvpn"
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    print(f"{path.name} {digest} structure/modes/architecture OK")
