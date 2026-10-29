"""Servers for the refusal probe in test-templates/refusal-probe.

Serves the templates directory over https on :8443, so net.html runs on a
secure origin, and answers anything on 127.0.0.1:18081, the plain http and
ws target every probe aims at. Each request to the latter is appended to
the hit log, and a WebSocket handshake is completed: the probe passes
only with that log empty.

    refusal-probe-server.py <templates-dir> <cert> <key> <hit-log>
"""

import base64
import functools
import hashlib
import http.server
import ssl
import sys
import threading

templates, cert, key, hits = sys.argv[1:5]


class Templates(http.server.SimpleHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass


class Target(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        with open(hits, "a") as f:
            f.write(self.path + "\n")
        key = self.headers.get("Sec-WebSocket-Key")
        if key:
            # Completes the handshake, so a page sees a ws:// that got
            # through open rather than fail.
            accept = base64.b64encode(hashlib.sha1(
                (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest())
            self.send_response(101)
            self.send_header("Upgrade", "websocket")
            self.send_header("Connection", "Upgrade")
            self.send_header("Sec-WebSocket-Accept", accept.decode())
            self.end_headers()
            return
        body = b"self.leaked = 'LEAKED';\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/javascript")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        pass


target = http.server.ThreadingHTTPServer(("127.0.0.1", 18081), Target)
threading.Thread(target=target.serve_forever, daemon=True).start()

tls = http.server.ThreadingHTTPServer(
    ("127.0.0.1", 8443), functools.partial(Templates, directory=templates))
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(cert, key)
tls.socket = ctx.wrap_socket(tls.socket, server_side=True)
print("serving", flush=True)
tls.serve_forever()
