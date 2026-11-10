#!/usr/bin/env python3
"""Structural checks for every SVG under brand/ (standard library only).

    python3 brand/scripts/validate.py

Each file must: parse as XML; have an <svg> root in the SVG namespace with a
viewBox; contain <title> and <desc>; have unique ids; contain no <script>,
<image>, <foreignObject>, event-handler attributes, editor namespaces or
metadata, external references (href/url() other than #fragment) or @font-face/
@import. Every brand SVG is meant to be font-independent, so any <text>,
<tspan>, <textPath> or font-family/font attribute fails. Files whose name contains "-mono" and every file in icons/ must use
only currentColor for fill and stroke. Exit status is non-zero on any failure.
"""
import os
import re
import sys
import xml.etree.ElementTree as ET

BRAND = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
SVG = "{http://www.w3.org/2000/svg}"
EDITOR = ("inkscape", "sodipodi", "sketch", "illustrator", "adobe", "figma", "serif.com")
FORBIDDEN = {"script", "image", "foreignObject", "metadata"}
FONT_DEPENDENT = {"text", "tspan", "textPath"}


def check(path):
    errs = []
    raw = open(path, encoding="utf-8").read()
    try:
        root = ET.fromstring(raw)
    except ET.ParseError as e:
        return [f"not well-formed: {e}"]
    if root.tag != SVG + "svg":
        errs.append("root is not <svg> in the SVG namespace")
    if not root.get("viewBox"):
        errs.append("missing viewBox")
    if root.find(SVG + "title") is None or not (root.find(SVG + "title").text or "").strip():
        errs.append("missing <title>")
    if root.find(SVG + "desc") is None:
        errs.append("missing <desc>")
    ids = [el.get("id") for el in root.iter() if el.get("id")]
    dup = {i for i in ids if ids.count(i) > 1}
    if dup:
        errs.append(f"duplicate ids: {sorted(dup)}")
    low = raw.lower()
    for e in EDITOR:
        if e in low:
            errs.append(f"editor trace: {e}")
    if "font-family" in low:
        errs.append("font-family in a style")
    if "@font-face" in low or "@import" in low:
        errs.append("font or stylesheet import")
    for el in root.iter():
        tag = el.tag.replace(SVG, "")
        if tag in FORBIDDEN:
            errs.append(f"forbidden element <{tag}>")
        if tag in FONT_DEPENDENT:
            errs.append(f"font-dependent element <{tag}> (draw text as outlines with outline_text.py)")
        for fa in ("font-family", "font"):
            if el.get(fa):
                errs.append(f"font-dependent attribute {fa}")
        for k, v in el.attrib.items():
            name = k.split("}")[-1]
            if name.startswith("on"):
                errs.append(f"event handler {name}")
            if name == "href" and not v.startswith("#"):
                errs.append(f"external href {v}")
            for u in re.findall(r"url\(([^)]*)\)", v):
                if not u.strip("'\" ").startswith("#"):
                    errs.append(f"external url({u})")
    mono = "-mono" in os.path.basename(path) or os.sep + "icons" + os.sep in path
    if mono:
        for el in root.iter():
            for attr in ("fill", "stroke", "color"):
                v = el.get(attr)
                if v and v not in ("currentColor", "none"):
                    errs.append(f"mono file uses {attr}={v}")
    return errs


def main():
    files = []
    for d, _, fs in os.walk(BRAND):
        if ".renders" in d:
            continue
        files += [os.path.join(d, f) for f in fs if f.endswith(".svg")]
    bad = 0
    for f in sorted(files):
        errs = check(f)
        rel = os.path.relpath(f, BRAND)
        print(("ok    " if not errs else "FAIL  ") + rel)
        for e in errs:
            print("        " + e)
        bad += bool(errs)
    print(f"\n{len(files)} files, {bad} failing")
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
