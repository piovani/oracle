#!/usr/bin/env python3
"""Helper for keeping docs/postman-collection.json in step with the oracle HTTP API.

Run from the repository root:

    python3 -I .claude/skills/postman-collection/scripts/api.py check
    python3 -I .claude/skills/postman-collection/scripts/api.py capture GET /health POST /health
    python3 -I .claude/skills/postman-collection/scripts/api.py test

check    compares the routes registered in internal/command/api.go with the collection and
         checks every request has a description, tests and saved examples. No server needed.
capture  starts the API against postgres-test, seeded with the sample quotes of scripts/seed
         (PETR4 and VALE3 on 2026-10-28..30, XPML11 on 2026-10-30), and prints the real response
         of each METHOD PATH, to copy into saved examples.
test     runs check, then starts the API and runs the collection with the Postman CLI.

The API is a freshly built binary on a free port, pointed at the postgres-test service of
docker-compose.yml (never the DB_* database). postgres-test is stopped at the end only if this
script started it.
"""

import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

COLLECTION = "docs/postman-collection.json"
ROUTES_FILE = "internal/command/api.go"
UNDOCUMENTED = ("/swagger/",)
# oracle_postman is recreated by scripts/seed with a few sample quotes before the API starts.
TEST_DB = {
    "DB_HOST": "localhost", "DB_PORT": "5433", "DB_USER": "oracle_test",
    "DB_PASSWORD": "oracle_test", "DB_NAME": "oracle_postman", "DB_SSLMODE": "disable",
}
SEED = "./.claude/skills/postman-collection/scripts/seed"
# Headers that change on every call, or that only answer urllib's Connection: close,
# and would only add noise to a saved example.
VOLATILE = {"date", "content-length", "connection"}


def fail(msg):
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(1)


def routes():
    """Routes registered on the mux, as {(METHOD, path)}. A pattern without a method is ANY."""
    src = open(ROUTES_FILE, encoding="utf-8").read()
    found = set()
    for pattern in re.findall(r'\.Handle(?:Func)?\(\s*"([^"]+)"', src):
        parts = pattern.split(" ", 1)
        method, path = (parts[0], parts[1]) if len(parts) == 2 else ("ANY", parts[0])
        if path.startswith(UNDOCUMENTED):
            continue
        found.add((method, re.sub(r"\{(\w+)(\.\.\.)?\}", r":\1", path)))
    return found


def items(node):
    """Every request item of the collection, flattening folders."""
    for item in node.get("item", []):
        if "request" in item:
            yield item
        else:
            yield from items(item)


def request_path(req):
    url = req["url"]
    raw = url if isinstance(url, str) else url.get("raw", "")
    return "/" + raw.replace("{{baseUrl}}", "").split("?", 1)[0].lstrip("/")


def check():
    try:
        collection = json.load(open(COLLECTION, encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as e:
        fail(f"{COLLECTION}: {e}")

    problems = []
    if "v2.1.0" not in collection.get("info", {}).get("schema", ""):
        problems.append("info.schema must be the Collection v2.1.0 schema")
    if not any(v.get("key") == "baseUrl" for v in collection.get("variable", [])):
        problems.append("collection variable baseUrl is missing")

    documented = set()
    for item in items(collection):
        name, req = item.get("name", "?"), item["request"]
        documented.add((req.get("method", "GET"), request_path(req)))
        if not req.get("description"):
            problems.append(f"{name}: request has no description")
        if "{{baseUrl}}" not in json.dumps(req.get("url")):
            problems.append(f"{name}: url does not start with {{{{baseUrl}}}}")
        if not any(e.get("listen") == "test" and e.get("script", {}).get("exec") for e in item.get("event", [])):
            problems.append(f"{name}: no test script")
        codes = [r.get("code") for r in item.get("response", [])]
        if not codes:
            problems.append(f"{name}: no saved examples")
        elif all(c < 400 for c in codes):
            problems.append(f"{name}: no error example (only {codes})")

    registered = routes()
    for method, path in sorted(registered - documented):
        if method == "ANY" and any(p == path for _, p in documented):
            continue
        problems.append(f"route {method} {path} is registered in {ROUTES_FILE} but not in the collection")
    for method, path in sorted(documented - registered):
        if ("ANY", path) not in registered:
            problems.append(f"request {method} {path} is in the collection but not registered in {ROUTES_FILE}")

    if problems:
        print("\n".join(f"- {p}" for p in problems))
        fail(f"{len(problems)} problem(s) in {COLLECTION}")
    print(f"check ok: {len(documented)} request(s), {len(registered)} route(s)")


class Server:
    """The API built from the working tree, running against postgres-test."""

    def __enter__(self):
        self.started_db = not subprocess.run(
            ["docker", "compose", "ps", "-q", "--status", "running", "postgres-test"],
            capture_output=True, text=True, check=False).stdout.strip()
        db = subprocess.run(["make", "test-db"], capture_output=True, text=True, check=False)
        if db.returncode != 0:
            fail("could not start postgres-test (is Docker running?):\n" + db.stdout + db.stderr)

        seed = subprocess.run(["go", "run", SEED], capture_output=True, text=True, check=False)
        if seed.returncode != 0:
            fail("could not seed oracle_postman:\n" + seed.stdout + seed.stderr)

        self.dir = tempfile.mkdtemp(prefix="oracle-postman-")
        binary = os.path.join(self.dir, "oracle")
        subprocess.run(["go", "build", "-o", binary, "./cmd/oracle"], check=True)

        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        self.base_url = f"http://127.0.0.1:{port}"
        self.log = open(os.path.join(self.dir, "api.log"), "w+")
        env = {**os.environ, **TEST_DB, "PORT": str(port)}
        # cwd is the temp dir so the .env of the repository is not picked up.
        self.proc = subprocess.Popen([binary, "api"], env=env, cwd=self.dir, stdout=self.log, stderr=self.log)

        deadline = time.time() + 15
        while time.time() < deadline:
            if self.proc.poll() is not None:
                break
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                    return self
            except OSError:
                time.sleep(0.1)
        self.log.seek(0)
        self.__exit__(None, None, None)
        fail("the API did not start:\n" + self.log.read())

    def __exit__(self, *_):
        # oracle api traps Ctrl+C without shutting down, so it is killed.
        if self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait()
        self.log.close()
        shutil.rmtree(self.dir, ignore_errors=True)
        if self.started_db:
            subprocess.run(["make", "test-db-down"], check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def capture(args):
    if not args or len(args) % 2:
        fail("capture takes pairs of METHOD PATH, e.g. capture GET /health POST /health")
    pairs = [(args[i].upper(), args[i + 1]) for i in range(0, len(args), 2)]
    with Server() as server:
        results = []
        for method, path in pairs:
            req = urllib.request.Request(server.base_url + path, method=method)
            try:
                resp = urllib.request.urlopen(req, timeout=10)
            except urllib.error.HTTPError as e:
                resp = e
            body = resp.read().decode("utf-8", "replace")
            results.append({
                "request": f"{method} {path}",
                "code": resp.status,
                "status": resp.reason,
                "header": [{"key": k, "value": v} for k, v in resp.headers.items() if k.lower() not in VOLATILE],
                "body": body,
            })
    print(json.dumps(results, indent=2, ensure_ascii=False))


def test():
    check()
    if not shutil.which("postman"):
        fail("Postman CLI not found; install it from https://learning.postman.com/docs/postman-cli/postman-cli-installation/")
    with Server() as server:
        run = subprocess.run(["postman", "collection", "run", COLLECTION, "--env-var", f"baseUrl={server.base_url}"], check=False)
    sys.exit(run.returncode)


def main():
    if not os.path.exists("go.mod") or not os.path.exists(ROUTES_FILE):
        fail("run from the repository root")
    cmd, args = (sys.argv[1], sys.argv[2:]) if len(sys.argv) > 1 else ("", [])
    if cmd == "check":
        check()
    elif cmd == "capture":
        capture(args)
    elif cmd == "test":
        test()
    else:
        print(__doc__)
        sys.exit(2)


if __name__ == "__main__":
    main()
