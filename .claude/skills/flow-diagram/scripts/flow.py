#!/usr/bin/env python3
"""Checks docs/flow.html against the code. Run from the repository root:

    python3 -I .claude/skills/flow-diagram/scripts/flow.py check
    python3 -I .claude/skills/flow-diagram/scripts/flow.py png

`check` compares the commands in cmd/oracle/main.go, the routes in internal/command/api.go and the
packages in internal/ with the picker buttons, the step texts and the nodes of the diagram, and
checks that the script of the page is valid JavaScript (needs node).

`png` renders the diagram to docs/flow-light.png and docs/flow-dark.png, the images in the README,
with headless Google Chrome (set CHROME to use another binary).
"""
import os
import pathlib
import re
import subprocess
import sys
import tempfile

ROOT = pathlib.Path.cwd()
PAGE = ROOT / "docs" / "flow.html"
SKIP_PACKAGES = {"command"}  # one function per command, drawn as the command nodes
SKIP_ROUTES = ("/health", "/swagger")  # not part of the data the steps describe


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def png():
    chrome = os.environ.get("CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
    if not pathlib.Path(chrome).exists():
        sys.exit(f"Chrome not found at {chrome}: set CHROME to a Chrome or Chromium binary")
    for theme in ("light", "dark"):
        out = ROOT / "docs" / f"flow-{theme}.png"
        subprocess.run([chrome, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                        "--force-device-scale-factor=2", "--window-size=980,860",
                        "--virtual-time-budget=5000", f"--screenshot={out}",
                        f"{PAGE.as_uri()}?shot&theme={theme}"], check=True, capture_output=True)
        print(f"wrote {out.relative_to(ROOT)}")


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in ("check", "png"):
        sys.exit(__doc__)
    if not PAGE.exists():
        sys.exit("docs/flow.html not found: run from the repository root")
    if sys.argv[1] == "png":
        png()
        return
    page = PAGE.read_text(encoding="utf-8")
    problems = []

    commands = set(re.findall(r'^\s*"(\w+)":\s+command\.', read("cmd/oracle/main.go"), re.M))
    buttons = set(re.findall(r'data-flow="(\w+)"', page)) - {"all"}
    step_keys = set(re.findall(r"^    (\w+): \{\s*$", page.split("var steps = {")[1].split("var stepsBox")[0], re.M)) - {"all"}
    for name in sorted(commands - buttons):
        problems.append(f"command {name}: no picker button in docs/flow.html")
    for name in sorted(commands - step_keys):
        problems.append(f"command {name}: no entry in steps")
    for name in sorted((buttons | step_keys) - commands):
        problems.append(f"{name}: in the page but not a command in cmd/oracle/main.go")

    api_steps = page.split("    api: {")[1].split("var stepsBox")[0] if "    api: {" in page else ""
    routes = re.findall(r'mux\.Handle(?:Func)?\("GET (/[^"]*)"', read("internal/command/api.go"))
    for route in sorted({"/" + r.strip("/").split("/")[0] for r in routes if not r.startswith(SKIP_ROUTES)}):
        if route not in api_steps:
            problems.append(f"route {route}: not mentioned in the steps of oracle api")

    for pkg in sorted(p.name for p in (ROOT / "internal").iterdir() if p.is_dir()):
        if pkg not in SKIP_PACKAGES and not any(m in page for m in (f"internal/{pkg}", f"{pkg}.", pkg.title())):
            problems.append(f"package internal/{pkg}: not in the page")

    flows = commands | {"all"}
    for flow_list in re.findall(r"flows: \[([^\]]*)\]", page):
        for name in re.findall(r'"(\w+)"', flow_list):
            if name not in flows:
                problems.append(f'flows lists "{name}", which is not a command')

    script = page.split("<script>")[1].split("</script>")[0]
    with tempfile.NamedTemporaryFile("w", suffix=".js", delete=False) as tmp:
        tmp.write(script)
    result = subprocess.run(["node", "--check", tmp.name], capture_output=True, text=True)
    pathlib.Path(tmp.name).unlink()
    if result.returncode != 0:
        problems.append("script is not valid JavaScript:\n" + result.stderr.strip())

    if problems:
        print("\n".join(problems))
        sys.exit(1)
    print(f"check ok: {len(commands)} command(s), {len(routes)} route(s)")


main()
