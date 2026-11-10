#!/usr/bin/env python3
"""Convert text to SVG outlines from an installed OpenType font (standard library only).

Brand graphics must render identically with no fonts installed, so they carry
no <text>: every string is drawn from glyph outlines extracted here. Supports
CFF-flavoured OpenType (Inter, .otf) and TrueType (JetBrains Mono, .ttf), with
pair kerning from GPOS. The fonts are only needed to *regenerate* artwork.

Library use (see build_sheets.py):
    from outline_text import Typesetter
    ts = Typesetter()                      # collects glyph definitions
    svg_fragment, width = ts.run("Hello", font="sans-600", size=24, x=10, y=40, fill="#000")
    defs = ts.defs()                       # <path id="g-..."> definitions to put in <defs>

Command line (prints a self-contained <path>, for one-off replacements such as
a single command line):
    python3 brand/scripts/outline_text.py --font mono-500 --size 40 --x 96 --y 700 \
        --fill "#E8EBF2" --id command "npx your-command"

Fonts (SIL Open Font License 1.1): Inter / Inter Display by Rasmus Andersson,
JetBrains Mono by JetBrains. Outlines in artwork are permitted by the OFL.
"""
import argparse
import os
import struct
import subprocess

FONT_FILES = {
    "sans-400": ("Inter", "Regular", "/usr/share/fonts/opentype/inter/Inter-Regular.otf"),
    "sans-500": ("Inter", "Medium", "/usr/share/fonts/opentype/inter/Inter-Medium.otf"),
    "sans-600": ("Inter", "SemiBold", "/usr/share/fonts/opentype/inter/Inter-SemiBold.otf"),
    "display-600": ("Inter Display", "SemiBold", "/usr/share/fonts/opentype/inter/InterDisplay-SemiBold.otf"),
    "display-700": ("Inter Display", "Bold", "/usr/share/fonts/opentype/inter/InterDisplay-Bold.otf"),
    "mono-400": ("JetBrains Mono", "Regular", "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Regular.ttf"),
    "mono-500": ("JetBrains Mono", "Medium", "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Medium.ttf"),
}


def locate(key):
    family, style, path = FONT_FILES[key]
    if os.path.exists(path):
        return path
    out = subprocess.run(["fc-match", "-f", "%{file}", f"{family}:style={style}"],
                         capture_output=True, text=True).stdout.strip()
    if family.split()[0].lower() not in os.path.basename(out).lower():
        raise SystemExit(f"font {family} {style} not found; install it to regenerate artwork")
    return out


class Font:
    def __init__(self, path):
        self.data = open(path, "rb").read()
        d = self.data
        n = struct.unpack(">H", d[4:6])[0]
        self.tables = {}
        for i in range(n):
            tag, _, off, ln = struct.unpack(">4sIII", d[12 + 16 * i: 28 + 16 * i])
            self.tables[tag.decode("latin-1")] = (off, ln)
        self.upm = self.u16(self.tables["head"][0] + 18)
        self._cmap()
        self._hmtx()
        self.kern = self._gpos_kern()
        self.cache = {}
        if "CFF " in self.tables:
            self._cff()
            self.kind = "cff"
        else:
            self.loc_fmt = self.i16(self.tables["head"][0] + 50)
            self.kind = "glyf"

    # -- binary helpers
    def u8(self, o): return self.data[o]
    def u16(self, o): return struct.unpack(">H", self.data[o:o + 2])[0]
    def i16(self, o): return struct.unpack(">h", self.data[o:o + 2])[0]
    def u32(self, o): return struct.unpack(">I", self.data[o:o + 4])[0]

    # -- cmap / metrics
    def _cmap(self):
        base = self.tables["cmap"][0]
        best = None
        for i in range(self.u16(base + 2)):
            pid, eid, off = struct.unpack(">HHI", self.data[base + 4 + 8 * i: base + 12 + 8 * i])
            fmt = self.u16(base + off)
            if pid in (0, 3) and fmt in (4, 12) and (best is None or fmt == 12):
                best = (base + off, fmt)
        o, fmt = best
        self.cmap = {}
        if fmt == 12:
            for g in range(self.u32(o + 12)):
                s, e, start = struct.unpack(">III", self.data[o + 16 + 12 * g: o + 28 + 12 * g])
                for c in range(s, min(e, 0x10FFFF) + 1):
                    self.cmap[c] = start + c - s
        else:
            seg = self.u16(o + 6) // 2
            ends, starts, deltas, ro = o + 14, o + 16 + 2 * seg, o + 16 + 4 * seg, o + 16 + 6 * seg
            for s in range(seg):
                e, st, de, r = self.u16(ends + 2 * s), self.u16(starts + 2 * s), self.i16(deltas + 2 * s), self.u16(ro + 2 * s)
                for c in range(st, e + 1):
                    if c == 0xFFFF:
                        continue
                    if r == 0:
                        g = (c + de) & 0xFFFF
                    else:
                        g = self.u16(ro + 2 * s + r + 2 * (c - st))
                        g = (g + de) & 0xFFFF if g else 0
                    self.cmap[c] = g

    def _hmtx(self):
        nh = self.u16(self.tables["hhea"][0] + 34)
        o = self.tables["hmtx"][0]
        self.adv = [self.u16(o + 4 * i) for i in range(nh)]

    def advance(self, g):
        return self.adv[g] if g < len(self.adv) else self.adv[-1]

    # -- GPOS pair kerning
    def _coverage(self, o):
        fmt = self.u16(o)
        if fmt == 1:
            return {self.u16(o + 4 + 2 * i): i for i in range(self.u16(o + 2))}
        cov = {}
        for i in range(self.u16(o + 2)):
            s, e, idx = self.u16(o + 4 + 6 * i), self.u16(o + 6 + 6 * i), self.u16(o + 8 + 6 * i)
            for g in range(s, e + 1):
                cov[g] = idx + g - s
        return cov

    def _classdef(self, o):
        fmt, cls = self.u16(o), {}
        if fmt == 1:
            start = self.u16(o + 2)
            for i in range(self.u16(o + 4)):
                cls[start + i] = self.u16(o + 6 + 2 * i)
        else:
            for i in range(self.u16(o + 2)):
                s, e, c = self.u16(o + 4 + 6 * i), self.u16(o + 6 + 6 * i), self.u16(o + 8 + 6 * i)
                for g in range(s, e + 1):
                    cls[g] = c
        return cls

    @staticmethod
    def _vr_size(fmt):
        return 2 * bin(fmt).count("1")

    def _vr_xadv(self, o, fmt):
        if not fmt & 4:
            return 0
        return self.i16(o + 2 * bin(fmt & 3).count("1"))

    def _gpos_kern(self):
        pairs, classes = {}, []
        if "GPOS" not in self.tables:
            return (pairs, classes)
        g = self.tables["GPOS"][0]
        fl, ll = g + self.u16(g + 6), g + self.u16(g + 8)
        idx = set()
        for i in range(self.u16(fl)):
            tag = self.data[fl + 2 + 6 * i: fl + 6 + 6 * i]
            if tag == b"kern":
                f = fl + self.u16(fl + 6 + 6 * i)
                idx.update(self.u16(f + 4 + 2 * k) for k in range(self.u16(f + 2)))
        for li in sorted(idx):
            lo = ll + self.u16(ll + 2 + 2 * li)
            ltype = self.u16(lo)
            for s in range(self.u16(lo + 4)):
                st = lo + self.u16(lo + 6 + 2 * s)
                t = ltype
                if t == 9:
                    t, st = self.u16(st + 2), st + self.u32(st + 4)
                if t != 2:
                    continue
                fmt, cov = self.u16(st), self._coverage(st + self.u16(st + 2))
                vf1, vf2 = self.u16(st + 4), self.u16(st + 6)
                s1, s2 = self._vr_size(vf1), self._vr_size(vf2)
                if fmt == 1:
                    for first, ci in cov.items():
                        ps = st + self.u16(st + 10 + 2 * ci)
                        for r in range(self.u16(ps)):
                            ro = ps + 2 + r * (2 + s1 + s2)
                            key = (first, self.u16(ro))
                            if key not in pairs:
                                pairs[key] = self._vr_xadv(ro + 2, vf1)
                else:
                    cd1, cd2 = self._classdef(st + self.u16(st + 8)), self._classdef(st + self.u16(st + 10))
                    c1n, c2n = self.u16(st + 12), self.u16(st + 14)
                    classes.append((set(cov), cd1, cd2, c2n, st + 16, s1 + s2, vf1))
        return (pairs, classes)

    def kerning(self, a, b):
        pairs, classes = self.kern
        if (a, b) in pairs:
            return pairs[(a, b)]
        for cov, cd1, cd2, c2n, rec, size, vf1 in classes:
            if a in cov:
                o = rec + (cd1.get(a, 0) * c2n + cd2.get(b, 0)) * size
                return self._vr_xadv(o, vf1)
        return 0

    # -- CFF
    def _index(self, o):
        count = self.u16(o)
        if count == 0:
            return [], o + 2
        osz = self.u8(o + 2)
        def off(i):
            b = self.data[o + 3 + i * osz: o + 3 + (i + 1) * osz]
            return int.from_bytes(b, "big")
        base = o + 2 + (count + 1) * osz
        items = [(base + off(i), base + off(i + 1)) for i in range(count)]
        return items, base + off(count)

    def _dict(self, s, e):
        d, ops, o = {}, [], s
        while o < e:
            b = self.data[o]
            if b <= 21:
                if b == 12:
                    key, o = 1200 + self.data[o + 1], o + 2
                else:
                    key, o = b, o + 1
                d[key], ops = ops, []
            elif b == 28:
                ops.append(self.i16(o + 1)); o += 3
            elif b == 29:
                ops.append(struct.unpack(">i", self.data[o + 1:o + 5])[0]); o += 5
            elif b == 30:
                o += 1
                while True:
                    x = self.data[o]; o += 1
                    if (x & 0xF) == 0xF or (x >> 4) == 0xF:
                        break
                ops.append(0)
            elif 32 <= b <= 246:
                ops.append(b - 139); o += 1
            elif 247 <= b <= 250:
                ops.append((b - 247) * 256 + self.data[o + 1] + 108); o += 2
            elif 251 <= b <= 254:
                ops.append(-(b - 251) * 256 - self.data[o + 1] - 108); o += 2
            else:
                o += 1
        return d

    def _cff(self):
        c = self.tables["CFF "][0]
        _, o = self._index(c + self.u8(c + 2))
        tops, o = self._index(o)
        _, o = self._index(o)
        self.gsubrs, _ = self._index(o)
        top = self._dict(*tops[0])
        self.charstrings, _ = self._index(c + top[17][0])
        self.fd_select = None
        if 1236 in top:  # CID-keyed: FDArray / FDSelect
            fds, _ = self._index(c + top[1236][0])
            self.fd_subrs = []
            for s, e in fds:
                fd = self._dict(s, e)
                size, off = fd[18]
                pd = self._dict(c + off, c + off + size)
                self.fd_subrs.append(self._index(c + off + pd[19][0])[0] if 19 in pd else [])
            fs = c + top[1237][0]
            fmt = self.u8(fs)
            self.fd_select = {}
            if fmt == 0:
                for gi in range(len(self.charstrings)):
                    self.fd_select[gi] = self.u8(fs + 1 + gi)
            else:
                nr = self.u16(fs + 1)
                for r in range(nr):
                    first, fd = self.u16(fs + 3 + 3 * r), self.u8(fs + 5 + 3 * r)
                    nxt = self.u16(fs + 6 + 3 * r)
                    for gi in range(first, nxt):
                        self.fd_select[gi] = fd
        else:
            size, off = top[18]
            pd = self._dict(c + off, c + off + size)
            self.subrs = self._index(c + off + pd[19][0])[0] if 19 in pd else []

    @staticmethod
    def _bias(n):
        return 107 if n < 1240 else 1131 if n < 33900 else 32768

    def _cff_glyph(self, gid):
        subrs = self.fd_subrs[self.fd_select.get(gid, 0)] if self.fd_select is not None else self.subrs
        path, st = [], {"x": 0, "y": 0, "stems": 0, "open": False, "width": False}
        stack = []

        def moveto(dx, dy):
            if st["open"]:
                path.append(("Z",))
            st["x"] += dx; st["y"] += dy
            path.append(("M", st["x"], st["y"])); st["open"] = True

        def lineto(dx, dy):
            st["x"] += dx; st["y"] += dy
            path.append(("L", st["x"], st["y"]))

        def curveto(a, b, c_, d, e, f):
            x1, y1 = st["x"] + a, st["y"] + b
            x2, y2 = x1 + c_, y1 + d
            st["x"], st["y"] = x2 + e, y2 + f
            path.append(("C", x1, y1, x2, y2, st["x"], st["y"]))

        def width_check(nargs_even):
            if not st["width"]:
                st["width"] = True
                if (len(stack) % 2 == 1) == nargs_even:
                    stack.pop(0)

        def run(s, e):
            o = s
            while o < e:
                b = self.data[o]
                if b >= 32 or b == 28 or b == 255:
                    if b == 28:
                        stack.append(self.i16(o + 1)); o += 3
                    elif b == 255:
                        stack.append(struct.unpack(">i", self.data[o + 1:o + 5])[0] / 65536); o += 5
                    elif b <= 246:
                        stack.append(b - 139); o += 1
                    elif b <= 250:
                        stack.append((b - 247) * 256 + self.data[o + 1] + 108); o += 2
                    else:
                        stack.append(-(b - 251) * 256 - self.data[o + 1] - 108); o += 2
                    continue
                o += 1
                if b in (1, 3, 18, 23):
                    width_check(True)
                    st["stems"] += len(stack) // 2; stack.clear()
                elif b in (19, 20):
                    width_check(True)
                    st["stems"] += len(stack) // 2; stack.clear()
                    o += (st["stems"] + 7) // 8
                elif b == 21:
                    width_check(True)
                    moveto(stack[-2], stack[-1]); stack.clear()
                elif b == 22:
                    width_check(False)
                    moveto(stack[-1], 0); stack.clear()
                elif b == 4:
                    width_check(False)
                    moveto(0, stack[-1]); stack.clear()
                elif b == 5:
                    for i in range(0, len(stack), 2):
                        lineto(stack[i], stack[i + 1])
                    stack.clear()
                elif b in (6, 7):
                    horiz = b == 6
                    for v in stack:
                        lineto(v, 0) if horiz else lineto(0, v)
                        horiz = not horiz
                    stack.clear()
                elif b == 8:
                    for i in range(0, len(stack), 6):
                        curveto(*stack[i:i + 6])
                    stack.clear()
                elif b == 27:  # hhcurveto
                    i, dy1 = 0, 0
                    if len(stack) % 2:
                        dy1, i = stack[0], 1
                    while i < len(stack):
                        curveto(stack[i], dy1, stack[i + 1], stack[i + 2], stack[i + 3], 0)
                        dy1 = 0; i += 4
                    stack.clear()
                elif b == 26:  # vvcurveto
                    i, dx1 = 0, 0
                    if len(stack) % 2:
                        dx1, i = stack[0], 1
                    while i < len(stack):
                        curveto(dx1, stack[i], stack[i + 1], stack[i + 2], 0, stack[i + 3])
                        dx1 = 0; i += 4
                    stack.clear()
                elif b in (30, 31):  # vhcurveto / hvcurveto
                    horiz = b == 31
                    i = 0
                    while i < len(stack):
                        last = len(stack) - i == 5
                        a = stack[i:i + 4]
                        extra = stack[i + 4] if last else 0
                        if horiz:
                            curveto(a[0], 0, a[1], a[2], extra, a[3])
                        else:
                            curveto(0, a[0], a[1], a[2], a[3], extra)
                        horiz = not horiz
                        i += 5 if last else 4
                    stack.clear()
                elif b == 24:  # rcurveline
                    i = 0
                    while len(stack) - i > 2:
                        curveto(*stack[i:i + 6]); i += 6
                    lineto(stack[i], stack[i + 1]); stack.clear()
                elif b == 25:  # rlinecurve
                    i = 0
                    while len(stack) - i > 6:
                        lineto(stack[i], stack[i + 1]); i += 2
                    curveto(*stack[i:i + 6]); stack.clear()
                elif b in (10, 29):
                    idx = int(stack.pop())
                    lst = subrs if b == 10 else self.gsubrs
                    s2, e2 = lst[idx + self._bias(len(lst))]
                    if run(s2, e2) == "end":
                        return "end"
                elif b == 11:
                    return
                elif b == 14:
                    width_check(True)
                    stack.clear()
                    return "end"
                elif b == 12:
                    b2 = self.data[o]; o += 1
                    s_ = stack
                    if b2 == 35:
                        curveto(*s_[0:6]); curveto(*s_[6:12])
                    elif b2 == 34:
                        curveto(s_[0], 0, s_[1], s_[2], s_[3], 0)
                        curveto(s_[4], 0, s_[5], -s_[2], s_[6], 0)
                    elif b2 == 36:
                        curveto(s_[0], s_[1], s_[2], s_[3], s_[4], 0)
                        curveto(s_[5], 0, s_[6], s_[7], s_[8], -(s_[1] + s_[3] + s_[7]))
                    elif b2 == 37:
                        dx = sum(s_[0:10:2]); dy = sum(s_[1:10:2])
                        sx, sy = st["x"], st["y"]
                        curveto(*s_[0:6])
                        if abs(dx) > abs(dy):
                            curveto(s_[6], s_[7], s_[8], s_[9], s_[10], sy - st["y"] - s_[7] - s_[9] + 0)
                        else:
                            curveto(s_[6], s_[7], s_[8], s_[9], sx - st["x"] - s_[6] - s_[8] + 0, s_[10])
                    stack.clear()
                else:
                    stack.clear()
        run(*self.charstrings[gid])
        if st["open"]:
            path.append(("Z",))
        return path

    # -- TrueType glyf
    def _glyf_glyph(self, gid):
        lo = self.tables["loca"][0]
        if self.loc_fmt == 0:
            s, e = self.u16(lo + 2 * gid) * 2, self.u16(lo + 2 * gid + 2) * 2
        else:
            s, e = self.u32(lo + 4 * gid), self.u32(lo + 4 * gid + 4)
        if s == e:
            return []
        o = self.tables["glyf"][0] + s
        nc = self.i16(o)
        if nc < 0:  # composite
            path, p = [], o + 10
            while True:
                flags, glyph = self.u16(p), self.u16(p + 2); p += 4
                if flags & 1:
                    dx, dy = self.i16(p), self.i16(p + 2); p += 4
                else:
                    dx, dy = struct.unpack(">bb", self.data[p:p + 2]); p += 2
                if flags & 8: p += 2
                elif flags & 0x40: p += 4
                elif flags & 0x80: p += 8
                for seg in self._glyf_glyph(glyph):
                    if seg[0] == "Z":
                        path.append(seg)
                    else:
                        pts = list(seg[1:])
                        path.append((seg[0], *[v + (dx if i % 2 == 0 else dy) for i, v in enumerate(pts)]))
                if not flags & 0x20:
                    break
            return path
        ends = [self.u16(o + 10 + 2 * i) for i in range(nc)]
        n = ends[-1] + 1
        p = o + 10 + 2 * nc
        p += 2 + self.u16(p)
        flags = []
        while len(flags) < n:
            f = self.data[p]; p += 1
            flags.append(f)
            if f & 8:
                r = self.data[p]; p += 1
                flags += [f] * r
        xs, ys, v = [], [], 0
        for f in flags:
            if f & 2:
                d = self.data[p]; p += 1
                v += d if f & 16 else -d
            elif not f & 16:
                v += self.i16(p); p += 2
            xs.append(v)
        v = 0
        for f in flags:
            if f & 4:
                d = self.data[p]; p += 1
                v += d if f & 32 else -d
            elif not f & 32:
                v += self.i16(p); p += 2
            ys.append(v)
        path, start = [], 0
        for end in ends:
            pts = [(xs[i], ys[i], flags[i] & 1) for i in range(start, end + 1)]
            start = end + 1
            # rotate so we start on an on-curve point (insert midpoint if none)
            k = next((i for i, q in enumerate(pts) if q[2]), None)
            if k is None:
                a, b = pts[0], pts[1]
                pts.insert(0, ((a[0] + b[0]) / 2, (a[1] + b[1]) / 2, 1)); k = 0
            pts = pts[k:] + pts[:k]
            path.append(("M", pts[0][0], pts[0][1]))
            i, m = 1, len(pts)
            ctrl = None
            for j in range(1, m + 1):
                q = pts[j % m]
                if q[2]:
                    if ctrl is None:
                        path.append(("L", q[0], q[1]))
                    else:
                        path.append(("Q", ctrl[0], ctrl[1], q[0], q[1])); ctrl = None
                else:
                    if ctrl is not None:
                        mx, my = (ctrl[0] + q[0]) / 2, (ctrl[1] + q[1]) / 2
                        path.append(("Q", ctrl[0], ctrl[1], mx, my))
                    ctrl = q
            path.append(("Z",))
        return path

    def glyph_path(self, gid):
        if gid not in self.cache:
            segs = self._cff_glyph(gid) if self.kind == "cff" else self._glyf_glyph(gid)
            out = []
            for s in segs:
                if s[0] == "Z":
                    out.append("Z")
                else:  # flip y (font y-up -> SVG y-down), whole font units
                    nums = [round(v) if i % 2 == 0 else round(-v) for i, v in enumerate(s[1:])]
                    out.append(s[0] + " ".join(str(v) for v in nums))
            self.cache[gid] = "".join(out)
        return self.cache[gid]


class Typesetter:
    """Lays out runs of text as <use> references to glyph outlines in font units."""

    def __init__(self, prefix="g"):
        self.fonts, self.used, self.prefix = {}, {}, prefix

    def font(self, key):
        if key not in self.fonts:
            self.fonts[key] = Font(locate(key))
        return self.fonts[key]

    def layout(self, text, font, tracking=0):
        """returns [(gid, x_in_font_units)], total advance in font units. tracking in 1/1000 em."""
        f = self.font(font)
        out, x, prev = [], 0, None
        for ch in text:
            g = f.cmap.get(ord(ch), 0)
            if prev is not None:
                x += f.kerning(prev, g) + tracking * f.upm / 1000
            out.append((g, x))
            x += f.advance(g)
            prev = g
        return out, x

    def width(self, text, font, size, tracking=0):
        f = self.font(font)
        return self.layout(text, font, tracking)[1] * size / f.upm

    def run(self, text, font, size, x, y, fill, anchor="start", tracking=0, rid=None):
        f = self.font(font)
        glyphs, adv = self.layout(text, font, tracking)
        s = size / f.upm
        w = adv * s
        if anchor == "end":
            x -= w
        elif anchor == "middle":
            x -= w / 2
        uses = []
        for g, gx in glyphs:
            d = f.glyph_path(g)
            if not d:
                continue
            gid = f"{self.prefix}-{font}-{g}"
            self.used[gid] = d
            uses.append(f'<use href="#{gid}" x="{round(gx)}"/>')
        idattr = f' id="{rid}"' if rid else ""
        label = text.replace("&", "&amp;").replace("<", "&lt;").replace('"', "&quot;")
        frag = (f'<g{idattr} transform="translate({x:.2f} {y:.2f}) scale({s:.5f})" fill="{fill}" '
                f'aria-label="{label}">' + "".join(uses) + "</g>")
        return frag, w

    def defs(self, indent="    "):
        return "\n".join(f'{indent}<path id="{k}" d="{v}"/>' for k, v in sorted(self.used.items()))


def single_path(text, font, size, x, y, fill, anchor="start", rid=None, tracking=0):
    """One self-contained <path> (absolute coordinates) for a run of text."""
    f = Font(locate(font))
    ts = Typesetter()
    ts.fonts[font] = f
    glyphs, adv = ts.layout(text, font, tracking)
    s = size / f.upm
    if anchor == "end":
        x -= adv * s
    elif anchor == "middle":
        x -= adv * s / 2
    parts = []
    for g, gx in glyphs:
        segs = f._cff_glyph(g) if f.kind == "cff" else f._glyf_glyph(g)
        for seg in segs:
            if seg[0] == "Z":
                parts.append("Z")
                continue
            nums = []
            for i, v in enumerate(seg[1:]):
                nums.append(round(x + (gx + v) * s, 2) if i % 2 == 0 else round(y - v * s, 2))
            parts.append(seg[0] + " ".join(f"{n:g}" for n in nums))
    idattr = f' id="{rid}"' if rid else ""
    label = text.replace("&", "&amp;").replace("<", "&lt;").replace('"', "&quot;")
    return f'<path{idattr} aria-label="{label}" fill="{fill}" d="{"".join(parts)}"/>', adv * s


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("text")
    ap.add_argument("--font", default="sans-500", choices=sorted(FONT_FILES))
    ap.add_argument("--size", type=float, default=24)
    ap.add_argument("--x", type=float, default=0)
    ap.add_argument("--y", type=float, default=0, help="baseline")
    ap.add_argument("--fill", default="currentColor")
    ap.add_argument("--anchor", default="start", choices=["start", "middle", "end"])
    ap.add_argument("--id", default=None)
    a = ap.parse_args()
    frag, w = single_path(a.text, a.font, a.size, a.x, a.y, a.fill, a.anchor, a.id)
    print(frag)
    print(f"<!-- width {w:.1f} -->")
