#!/usr/bin/env python3
"""Adds android:foregroundServiceType to services in a compiled (binary) AndroidManifest.xml.

    manifest_fgs.py <AndroidManifest.xml (binary, in place)> <Service=type,...>     e.g. .InstallService=0x1

Only for build_apk.sh's Ubuntu route: its aapt (API 23 android.jar) does not know the attribute (API 29), and an app that
targets Android 14+ must declare it. The Android SDK route compiles the attribute directly and never runs this. Needs pyaxml
(pip install pyaxml). Every attribute that was there before must come out with the same value: a mismatch stops the build.
"""
import sys

import pyaxml
from lxml import etree

A = "{http://schemas.android.com/apk/res/android}"


def attrs(root):
    out = []
    for el in root.iter():
        for k, v in sorted(el.attrib.items()):
            if v in ("true", "-1", "0xffffffff"):  # aapt writes true as -1, pyaxml as 1: the same to Android
                v = "1"
            out.append((el.tag, el.get(A + "name"), k, v))
    return out


def main():
    path, spec = sys.argv[1], sys.argv[2]
    want = dict(s.split("=", 1) for s in spec.split(","))
    a = pyaxml.AXML.from_axml(open(path, "rb").read())
    x = a.to_xml()
    before = attrs(x)
    done = set()
    for svc in x.iter("service"):
        name = svc.get(A + "name")
        if name in want:
            svc.set(A + "foregroundServiceType", want[name])
            done.add(name)
    if done != set(want):
        sys.exit("no <service> named %s in the manifest" % ", ".join(sorted(set(want) - done)))
    b = pyaxml.AXML()
    b.from_xml(x)
    b.compute()
    data = b.pack()
    after = attrs(pyaxml.AXML.from_axml(data).to_xml())
    added = [t for t in after if t not in before]
    lost = [t for t in before if t not in after]
    if lost or any(t[2] != A + "foregroundServiceType" for t in added) or len(added) != len(want):
        sys.exit("manifest changed beyond foregroundServiceType: lost %s, added %s" % (lost, added))
    open(path, "wb").write(data)


if __name__ == "__main__":
    main()
