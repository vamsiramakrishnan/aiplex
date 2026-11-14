"""The client does not follow redirects: the token never reaches a redirect target."""

from __future__ import annotations

import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import httpx
import pytest

import aiplex.client as client_module
from aiplex import AiplexError, Budget, Client

TOKEN = "tok-redirect-test-0123456789"


class _Server:
    """A local server answering every request with `status` and `location`, or counting hits."""

    def __init__(self, status: int = 200, location: str | None = None) -> None:
        self.hits = 0
        self.auth: list[str | None] = []
        self.status = status
        self.location = location
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, format: str, *args: object) -> None:
                return

            def _handle(self) -> None:
                length = int(self.headers.get("Content-Length", "0"))
                if length:
                    self.rfile.read(length)
                outer.hits += 1
                outer.auth.append(self.headers.get("Authorization"))
                self.send_response(outer.status)
                loc = outer.location(self) if callable(outer.location) else outer.location
                if loc is not None:
                    self.send_header("Location", loc)
                self.send_header("Content-Length", "2")
                self.end_headers()
                self.wfile.write(b"{}")

            do_GET = do_POST = do_PUT = do_DELETE = _handle

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.port = self.server.server_address[1]
        self.url = f"http://127.0.0.1:{self.port}"
        self.other_host_url = f"http://localhost:{self.port}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()


@pytest.fixture
def sink():
    s = _Server(200)
    yield s
    s.close()


def _api_redirecting_to(target: str, status: int):
    api = _Server(status, target + "/leak/SECRET?token=SECRET")
    return api


def _check(err: AiplexError, status: int, target: str) -> None:
    assert err.code == "redirect_refused"
    assert err.status_code == status
    scheme_host = target.split("/", 3)[0] + "//" + target.split("/")[2]
    assert scheme_host in err.message
    assert "AIPLEX_URL" in err.message
    for text in (str(err), repr(err), err.message, str(err.details)):
        assert "SECRET" not in text
        assert "/leak" not in text


@pytest.mark.parametrize("status", [301, 302, 303, 307, 308])
@pytest.mark.parametrize("other_host", [False, True])
def test_redirect_is_refused_and_target_gets_nothing(sink: _Server, status: int, other_host: bool) -> None:
    target = sink.other_host_url if other_host else sink.url
    api = _api_redirecting_to(target, status)
    try:
        with Client(api.url, TOKEN) as client:
            with pytest.raises(AiplexError) as get_err:
                client.get_budget_status("b")
            _check(get_err.value, status, target)
            with pytest.raises(AiplexError) as put_err:
                client.put_budget(Budget(id="b"))
            _check(put_err.value, status, target)
            with pytest.raises(AiplexError) as del_err:
                client.delete_budget("b", 1)
            _check(del_err.value, status, target)
    finally:
        api.close()
    assert sink.hits == 0
    assert api.hits == 3


@pytest.mark.parametrize(
    ("status", "location", "expect_code", "expect_in", "expect_not_in"),
    [
        (301, "/other/SECRET?token=SECRET", "redirect_refused", "http://127.0.0.1:", "SECRET"),
        (302, "//elsewhere.example/p?token=SECRET", "redirect_refused", "elsewhere.example", "SECRET"),
        (307, None, "redirect_refused", "no usable Location", ""),
        (308, "mailto:a@b.example?token=SECRET", "redirect_refused", "no usable Location", "SECRET"),
        (302, "http://" + "a" * 2000 + ".example/p?token=SECRET", "redirect_refused", "no usable Location", "aaaaaaaaaa"),
        (304, "http://elsewhere.example/", "unknown", "", ""),
    ],
)
def test_redirect_edge_cases(status, location, expect_code, expect_in, expect_not_in) -> None:
    api = _Server(status, location)
    try:
        with Client(api.url, TOKEN) as client:
            with pytest.raises(AiplexError) as exc:
                client.get_budget_status("b")
    finally:
        api.close()
    err = exc.value
    assert err.code == expect_code
    assert err.status_code == status
    assert expect_in in err.message
    if expect_not_in:
        assert expect_not_in not in str(err)


def test_caller_supplied_client_is_left_alone(sink: _Server) -> None:
    api = _api_redirecting_to(sink.url, 302)
    try:
        custom = httpx.Client(follow_redirects=True)
        with Client(api.url, TOKEN, http_client=custom) as client:
            # The caller's policy stands: the SDK does not edit it.
            try:
                client.get_budget_status("b")
            except AiplexError:
                pass
        assert custom.follow_redirects is True
        assert sink.hits == 1
        custom.close()
    finally:
        api.close()


def test_caller_supplied_client_that_does_not_follow_gets_the_typed_error(sink: _Server) -> None:
    api = _api_redirecting_to(sink.url, 307)
    try:
        custom = httpx.Client(follow_redirects=False)
        with Client(api.url, TOKEN, http_client=custom) as client:
            try:
                client.get_budget_status("b")
            except AiplexError as err:
                assert err.code == "redirect_refused"
                assert TOKEN not in str(err)
            else:
                raise AssertionError("a redirect was not refused")
        assert sink.hits == 0
        custom.close()
    finally:
        api.close()


def test_default_client_does_not_follow_redirects() -> None:
    client = Client("http://127.0.0.1:1", TOKEN)
    try:
        assert client._client.follow_redirects is False
    finally:
        client.close()


def test_many_refused_redirects_do_not_exhaust_the_connection_pool() -> None:
    # httpx closes the response when a response event hook raises
    # (httpx/_client.py, _send_handling_redirects: `except BaseException: response.close()`).
    # A pool of one connection with no pool timeout would block or raise PoolTimeout otherwise.
    api = _Server(302, "http://127.0.0.1:1/x?token=SECRET")
    client = Client(api.url, TOKEN)
    try:
        client._client.close()
        client._client = httpx.Client(
            timeout=httpx.Timeout(5.0, pool=0.5),
            follow_redirects=False,
            limits=httpx.Limits(max_connections=1, max_keepalive_connections=1),
            event_hooks=client_module._OWNED_HOOKS,
        )
        for _ in range(50):
            with pytest.raises(AiplexError) as exc:
                client.get_budget_status("b")
            assert exc.value.code == "redirect_refused"
    finally:
        client.close()
        api.close()
    assert api.hits == 50
