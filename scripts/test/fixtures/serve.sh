#!/bin/sh
# serve.sh — Start a local HTTP server serving the fixtures/release directory.
#
# Usage:
#   PORT=<port> RELEASE_DIR=<path> sh serve.sh
#
# The server maps:
#   /api/repos/<repo>/releases/latest  -> latest.json
#   /releases/download/<ver>/<file>    -> fixtures/release/<ver>/<file>
#
# This is a minimal HTTP server using Python's http.server with a custom
# handler that rewrites paths to match what install.sh expects.
#
# Outputs the PID to stdout so the caller can kill it when done.

PORT="${PORT:-18080}"
RELEASE_DIR="${RELEASE_DIR:-$(dirname "$0")/release}"

# Write a tiny Python server to a temp file and run it.
server_script=$(mktemp /tmp/agentbox-test-server.XXXXXX.py)
cat > "$server_script" <<PYEOF
import http.server
import os
import sys
import json
import re

PORT = int(sys.argv[1])
RELEASE_DIR = sys.argv[2]

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # suppress access log

    def send_file(self, path):
        if not os.path.isfile(path):
            self.send_error(404, "Not found: " + path)
            return
        size = os.path.getsize(path)
        self.send_response(200)
        self.send_header("Content-Length", str(size))
        self.end_headers()
        with open(path, "rb") as f:
            self.wfile.write(f.read())

    def do_GET(self):
        path = self.path.split("?")[0]

        # /api/repos/<anything>/releases/latest -> latest.json
        m = re.match(r'^/api/.*?/releases/latest$', path)
        if m:
            # find latest.json in any version dir (use most recent)
            latest_json = None
            for ver in sorted(os.listdir(RELEASE_DIR), reverse=True):
                candidate = os.path.join(RELEASE_DIR, ver, "latest.json")
                if os.path.isfile(candidate):
                    latest_json = candidate
                    break
            if latest_json:
                self.send_file(latest_json)
            else:
                self.send_error(500, "no latest.json found")
            return

        # /releases/download/<version>/<filename>
        m = re.match(r'^/releases/download/([^/]+)/(.+)$', path)
        if m:
            ver, fname = m.group(1), m.group(2)
            fpath = os.path.join(RELEASE_DIR, ver, fname)
            self.send_file(fpath)
            return

        self.send_error(404, "unrecognized path")

httpd = http.server.HTTPServer(("127.0.0.1", PORT), Handler)
httpd.serve_forever()
PYEOF

python3 "$server_script" "$PORT" "$RELEASE_DIR" &
SERVER_PID=$!
echo "$SERVER_PID"
echo "$server_script"
