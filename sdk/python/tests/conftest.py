from __future__ import annotations

import json
import threading
from collections import defaultdict, deque
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs, urlsplit

import pytest

from aiplex import Client


@dataclass
class RecordedRequest:
    method: str
    path: str
    query: dict[str, str]
    headers: dict[str, str]
    body: Any


@dataclass
class CannedResponse:
    status: int = 200
    json_body: Any = None
    raw_body: bytes | None = None
    content_type: str = "application/json"


class FakeAPI:
    def __init__(self) -> None:
        self._routes: dict[tuple[str, str], deque[CannedResponse]] = defaultdict(deque)
        self._lock = threading.Lock()
        self.requests: list[RecordedRequest] = []

        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, format: str, *args: object) -> None:
                return

            def _handle(self) -> None:
                parts = urlsplit(self.path)
                path = parts.path
                parsed_qs = parse_qs(parts.query, keep_blank_values=True)
                query = {k: v[-1] for k, v in parsed_qs.items()}
                headers = {k.lower(): v for k, v in self.headers.items()}

                length = int(self.headers.get("Content-Length", "0"))
                raw_bytes = self.rfile.read(length) if length > 0 else b""
                parsed_body: Any = None
                if raw_bytes:
                    try:
                        parsed_body = json.loads(raw_bytes.decode("utf-8"))
                    except Exception:
                        parsed_body = raw_bytes.decode("utf-8", errors="replace")

                with outer._lock:
                    outer.requests.append(
                        RecordedRequest(
                            method=self.command,
                            path=path,
                            query=query,
                            headers=headers,
                            body=parsed_body,
                        )
                    )
                    queue = outer._routes.get((self.command.upper(), path))
                    if queue:
                        resp = queue.popleft() if len(queue) > 1 else queue[0]
                    else:
                        resp = CannedResponse(
                            status=404,
                            json_body={"code": "NOT_FOUND", "message": "no fake route"},
                        )

                self.send_response(resp.status)
                if resp.status == 204 or (resp.json_body is None and resp.raw_body is None):
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return

                if resp.raw_body is not None:
                    payload = resp.raw_body
                else:
                    payload = json.dumps(resp.json_body).encode("utf-8")

                self.send_header("Content-Type", resp.content_type)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def do_GET(self) -> None:
                self._handle()

            def do_PUT(self) -> None:
                self._handle()

            def do_POST(self) -> None:
                self._handle()

            def do_DELETE(self) -> None:
                self._handle()

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        host, port = self._server.server_address[:2]
        self.url = f"http://{host}:{port}"
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    def route(
        self,
        method: str,
        path: str,
        *,
        status: int = 200,
        json: Any = None,
        raw_body: bytes | None = None,
        content_type: str = "application/json",
    ) -> None:
        with self._lock:
            self._routes[(method.upper(), path)].append(
                CannedResponse(
                    status=status,
                    json_body=json,
                    raw_body=raw_body,
                    content_type=content_type,
                )
            )

    def close(self) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5.0)


@pytest.fixture
def fake_api() -> FakeAPI:
    api = FakeAPI()
    try:
        yield api
    finally:
        api.close()


@pytest.fixture
def client(fake_api: FakeAPI) -> Client:
    c = Client(base_url=fake_api.url, token="test-token-123")
    try:
        yield c
    finally:
        c.close()
