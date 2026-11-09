"""EPUB validation."""

from __future__ import annotations

import posixpath
import zipfile
from pathlib import Path
from urllib.parse import unquote, urlsplit

from lxml import etree


def validate_epub(path: Path) -> bool:
    path = Path(path)
    try:
        if not path.exists() or path.stat().st_size == 0:
            return False

        with zipfile.ZipFile(path, "r") as archive:
            if archive.testzip() is not None:
                return False

            names = archive.namelist()
            if len(names) != len(set(names)):
                return False
            if "mimetype" not in names or "META-INF/container.xml" not in names:
                return False

            mimetype = archive.read("mimetype").decode("ascii", errors="ignore").strip()
            if mimetype != "application/epub+zip":
                return False

            container = etree.fromstring(archive.read("META-INF/container.xml"))
            rootfile = container.find(".//{*}rootfile")
            opf_name = rootfile.get("full-path") if rootfile is not None else None
            if not opf_name or opf_name not in names:
                opf_files = [name for name in names if name.endswith(".opf")]
                if not opf_files:
                    return False
                opf_name = opf_files[0]

            package = etree.fromstring(archive.read(opf_name))
            if package.tag.split("}")[-1] != "package":
                return False

            manifest = package.find(".//{*}manifest")
            spine = package.find(".//{*}spine")
            if manifest is None or spine is None:
                return False

            ids: set[str] = set()
            hrefs: set[str] = set()
            opf_dir = posixpath.dirname(opf_name)
            for item in manifest.findall("{*}item"):
                item_id = item.get("id")
                href = item.get("href")
                if not item_id or not href:
                    return False
                if item_id in ids or href in hrefs:
                    return False
                ids.add(item_id)
                hrefs.add(href)

                parsed_href = urlsplit(href)
                if parsed_href.scheme in ("http", "https", "data"):
                    continue
                href_path = unquote(parsed_href.path).lstrip("/")
                if not href_path:
                    return False
                target = posixpath.normpath(posixpath.join(opf_dir, href_path))
                if target not in names:
                    return False

            for itemref in spine.findall("{*}itemref"):
                if itemref.get("idref") not in ids:
                    return False

    except Exception:
        return False
    return True
