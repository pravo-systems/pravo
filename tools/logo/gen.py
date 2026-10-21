#!/usr/bin/env python3
"""Generate the pravo mark and the raster icons derived from it.

The mark is the 8-node lattice of the landing page (site/design/css/00-tokens.css,
--i-lattice) in the accent colour of the buttons, on the dark page colour.

    python3 tools/logo/gen.py            # writes SVGs, HTML and the PNG/ICO assets

Needs Google Chrome for the raster output (headless screenshots).
"""
import os, struct, subprocess, sys, tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SITE = os.path.join(ROOT, "site")
CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

INK, MINT, PAPER_GREEN, WHITE, MID = "#0b0d10", "#3ddbb0", "#0a735a", "#ffffff", "#14b088"

# Lattice in the coordinates of the landing graphic (viewBox 0 0 200 220).
NODES = [(100, 196), (40, 146), (100, 146), (160, 146), (40, 86), (100, 86), (160, 86), (100, 36)]
EDGES = [((100, 196), (40, 146)), ((100, 196), (100, 146)), ((100, 196), (160, 146)),
         ((40, 146), (40, 86)), ((40, 146), (100, 86)), ((100, 146), (40, 86)),
         ((100, 146), (160, 86)), ((160, 146), (100, 86)), ((160, 146), (160, 86)),
         ((40, 86), (100, 36)), ((100, 86), (100, 36)), ((160, 86), (100, 36))]


def mark(size, scale, node, top, stroke, edge_opacity, radius, bg=True, adaptive=False, tile=INK, fg=MINT):
    """SVG of the mark in a size x size box; scale maps lattice units to box units."""
    cx, cy = size / 2, size / 2
    ce, cn = ('class="e"', 'class="n"') if adaptive else (f'stroke="{fg}"', f'fill="{fg}"')
    def p(pt): return (cx + (pt[0] - 100) * scale, cy + (pt[1] - 116) * scale)
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}" width="{size}" height="{size}" role="img" aria-label="pravo">']
    if adaptive:
        # The header colours the mark with a CSS mask, so only the shape counts there;
        # opened on its own the file follows the colour scheme of the viewer.
        out.append(f"<style>.e{{stroke:#0a735a}}.n{{fill:#0a735a}}@media(prefers-color-scheme:dark){{.e{{stroke:{MINT}}}.n{{fill:{MINT}}}}}</style>")
    if bg:
        out.append(f'<rect width="{size}" height="{size}" rx="{radius}" fill="{tile}"/>')
    d = "".join(f"M{p(a)[0]:.2f} {p(a)[1]:.2f}L{p(b)[0]:.2f} {p(b)[1]:.2f}" for a, b in EDGES)
    out.append(f'<path d="{d}" fill="none" {ce} stroke-opacity="{edge_opacity}" stroke-width="{stroke}" stroke-linecap="square"/>')
    sq = []
    for n in NODES:
        s = top if n == (100, 36) else node
        x, y = p(n)
        sq.append(f"M{x - s / 2:.2f} {y - s / 2:.2f}h{s}v{s}h-{s}z")
    out.append(f'<path d="{"".join(sq)}" {cn}/>')
    out.append("</svg>")
    return "\n".join(out) + "\n"


def write(path, text):
    with open(path, "w") as f:
        f.write(text)


def chrome_png(html, out, w, h, transparent=False):
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False) as t:
        t.write(html)
    args = [CHROME, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--force-device-scale-factor=1",
            f"--window-size={w},{h}", f"--screenshot={out}", "--default-background-color=" + ("00000000" if transparent else "ff0b0d10"),
            "file://" + t.name]
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    os.unlink(t.name)


def page(body, css=""):
    return f"<!doctype html><meta charset=utf-8><style>html,body{{margin:0;background:transparent}}svg{{display:block}}{css}</style>{body}"


def ico(entries, out):
    """entries: list of (size, png bytes)."""
    head = struct.pack("<HHH", 0, 1, len(entries))
    off = 6 + 16 * len(entries)
    dirs, data = b"", b""
    for size, png in entries:
        dirs += struct.pack("<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32, len(png), off + len(data))
        data += png
    with open(out, "wb") as f:
        f.write(head + dirs + data)


def main():
    # The mark: no tile, so it takes the colour of the page. 64 box, lattice 58 tall.
    logo = mark(64, 0.33, 7, 9, 2.6, 0.72, 0, bg=False, adaptive=True)
    write(os.path.join(SITE, "assets", "logo.svg"), logo)

    # Small sizes: no tile, a green between the two themes so that the mark holds on light and dark
    # tab bars; heavier strokes and nodes so 16 px still reads as a lattice.
    small = mark(64, 0.36, 8, 10, 3.2, 1, 0, bg=False, fg=MID)
    # 16 px: the nodes alone, large, so that they stay separate dots.
    tiny = mark(64, 0.34, 12, 14, 2.4, 0.6, 0, bg=False, fg=MID)
    # Touch icon: iOS fills transparency with black, so this one keeps a white full-bleed tile.
    touch = mark(180, 0.90, 14, 18, 3.6, 0.8, 0, tile=WHITE, fg=PAPER_GREEN)
    tmp = tempfile.mkdtemp()
    png = {}
    for s in (16, 32, 48):
        chrome_png(page((tiny if s == 16 else small).replace('width="64" height="64"', f'width="{s}" height="{s}"', 1)), f"{tmp}/{s}.png", s, s, transparent=True)
        png[s] = open(f"{tmp}/{s}.png", "rb").read()
    ico([(s, png[s]) for s in (16, 32, 48)], os.path.join(SITE, "favicon.ico"))
    chrome_png(page(touch), os.path.join(SITE, "assets", "apple-touch-icon.png"), 180, 180)

    # Preview card: lattice on the right, wordmark, line and sub-line on the left.
    big = mark(560, 2.0, 30, 38, 6, 0.55, 0, bg=False)
    og = page(f'''<div class=c>
<div class=t><div class=w>pravo</div><div class=l>The declarative layer, reimagined.</div>
<div class=s>An open study, on the OpenTofu providers you already use.</div></div>{big}</div>''', css="""
@font-face{font-family:N;src:url(file://%(f)s/newsreader-400.woff2)}
@font-face{font-family:S;src:url(file://%(f)s/plex-sans-400.woff2)}
body{width:1200px;height:630px;background:#0b0d10;position:relative;overflow:hidden}
body:before{content:"";position:absolute;inset:0;background:
 linear-gradient(#1a1f27 1px,transparent 1px) 0 0/150px 105px,linear-gradient(90deg,#1a1f27 1px,transparent 1px) 0 0/150px 105px;opacity:.55}
.c{position:absolute;inset:80px;border:1px solid #2a313b;display:flex;align-items:center;justify-content:space-between;padding:0 56px 0 44px;box-sizing:border-box}
.t{position:relative}
.w{font:400 190px/1 N;color:#efe9d8;letter-spacing:-.02em;margin-bottom:34px}
.l{font:400 38px/1.2 N;color:#3ddbb0;margin-bottom:14px}
.s{font:400 23px/1.3 S;color:#9aa3b0;white-space:nowrap}
svg{position:relative;width:420px;height:420px}
""" % {"f": os.path.join(SITE, "assets", "fonts")})
    chrome_png(og, os.path.join(SITE, "assets", "og.png"), 1200, 630)
    print("wrote logo.svg, favicon.ico, apple-touch-icon.png, og.png")


if __name__ == "__main__":
    sys.exit(main())
