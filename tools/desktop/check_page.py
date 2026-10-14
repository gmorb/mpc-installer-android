#!/usr/bin/env python3
"""Checks that every inline <script> in web/index.html parses (node --check). One stray quote in a string stops the whole page:
nothing on it works, not even Connect. Run from anywhere: python3 tools/desktop/check_page.py (needs Node.js)."""
import os, re, subprocess, sys, tempfile

page = os.path.join(os.path.dirname(os.path.abspath(__file__)), "web", "index.html")
scripts = re.findall(r"<script>(.*?)</script>", open(page, encoding="utf-8").read(), re.S)
if not scripts:
    sys.exit("no inline scripts found in " + page)
bad = 0
with tempfile.TemporaryDirectory() as d:
    for i, js in enumerate(scripts):
        f = os.path.join(d, "script%d.js" % i)
        open(f, "w", encoding="utf-8").write(js)
        r = subprocess.run(["node", "--check", f], capture_output=True, text=True)
        if r.returncode:
            bad += 1
            print("script %d of %d does not parse:\n%s" % (i + 1, len(scripts), r.stderr.strip()))
print("%d inline script(s) checked, %d broken" % (len(scripts), bad))
sys.exit(1 if bad else 0)
