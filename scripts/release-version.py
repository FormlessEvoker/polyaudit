"""Compute a stable release version; reruns reuse the tag on this commit."""
import os
import re
import subprocess


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def version(tag):
    return tuple(map(int, tag[1:].split(".")))


tags = [t for t in git("tag", "--list").splitlines()
        if re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", t)]
latest = max(tags, key=version) if tags else None
head = git("rev-parse", "HEAD")
if latest and git("rev-list", "-n", "1", latest) == head:
    next_tag = latest  # Retry an interrupted or completed release.
else:
    if latest:
        subprocess.run(["git", "merge-base", "--is-ancestor", latest, head], check=True)
    major, minor, patch = version(latest) if latest else (0, 0, 0)
    if os.environ.get("MAJOR") == "true":
        major, minor, patch = major + 1, 0, 0
    elif os.environ.get("MINOR") == "true":
        minor, patch = minor + 1, 0
    else:
        patch += 1
    next_tag = f"v{major}.{minor}.{patch}"
print(f"next={next_tag}")
print(f"prerelease={str(version(next_tag)[0] == 0).lower()}")
