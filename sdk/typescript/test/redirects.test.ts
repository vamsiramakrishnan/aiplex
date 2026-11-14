import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import { AiplexClient, AiplexError } from "../src/index.js";

const TOKEN = "tok-redirect-test-0123456789";

interface Listener {
  server: Server;
  port: number;
  hits: { auth: string | undefined; method: string }[];
}

async function listen(
  handler: (res: import("node:http").ServerResponse) => void,
): Promise<Listener> {
  const hits: Listener["hits"] = [];
  const server = createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      hits.push({ auth: req.headers.authorization, method: req.method ?? "" });
      handler(res);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return { server, port: (server.address() as AddressInfo).port, hits };
}

const open: Listener[] = [];
afterEach(async () => {
  while (open.length > 0) {
    const l = open.pop()!;
    l.server.closeAllConnections();
    await new Promise((resolve) => l.server.close(resolve));
  }
});

async function pair(
  status: number,
  target: (sinkPort: number) => string,
): Promise<{ api: Listener; sink: Listener; location: string }> {
  const sink = await listen((res) => res.end("{}"));
  open.push(sink);
  const location = target(sink.port);
  const api = await listen((res) => {
    res.writeHead(status, { Location: `${location}/leak/SECRET?token=SECRET` });
    res.end();
  });
  open.push(api);
  return { api, sink, location };
}

function client(api: Listener): AiplexClient {
  return new AiplexClient({ baseUrl: `http://127.0.0.1:${api.port}`, token: TOKEN });
}

function expectRefused(err: unknown, status: number, location: string): void {
  expect(err).toBeInstanceOf(AiplexError);
  const e = err as AiplexError;
  expect(e.code).toBe("redirect_refused");
  expect(e.statusCode).toBe(status);
  expect(e.message).toContain(location);
  expect(e.message).toContain("AIPLEX_URL");
  for (const text of [e.message, String(e), JSON.stringify(e.details)]) {
    expect(text).not.toContain("SECRET");
    expect(text).not.toContain("/leak");
  }
}

describe("redirects are refused", () => {
  for (const status of [301, 302, 303, 307, 308]) {
    for (const otherHost of [false, true]) {
      it(`${status} ${otherHost ? "to another host" : "to another port"}`, async () => {
        const { api, sink, location } = await pair(
          status,
          (p) => `http://${otherHost ? "localhost" : "127.0.0.1"}:${p}`,
        );
        const c = client(api);

        const calls: Array<() => Promise<unknown>> = [
          () => c.getBudgetStatus("b"),
          () => c.putBudget({ id: "b" }),
          () => c.deleteBudget("b", 1),
        ];
        for (const call of calls) {
          let caught: unknown;
          try {
            await call();
          } catch (err) {
            caught = err;
          }
          expectRefused(caught, status, location);
        }
        expect(sink.hits).toHaveLength(0);
        expect(api.hits).toHaveLength(3);
      });
    }
  }
});

describe("redirect edge cases", () => {
  const cases: Array<[string, number, string | null, string, string, string]> = [
    ["relative Location", 301, "/other/SECRET?token=SECRET", "redirect_refused", "http://127.0.0.1:", "SECRET"],
    ["scheme-relative Location", 302, "//elsewhere.example/p?token=SECRET", "redirect_refused", "elsewhere.example", "SECRET"],
    ["missing Location", 307, null, "redirect_refused", "no usable Location", ""],
    ["Location without a host", 308, "mailto:a@b.example?token=SECRET", "redirect_refused", "no usable Location", "SECRET"],
    ["host longer than a DNS name", 302, `http://${"a".repeat(2000)}.example/p?token=SECRET`, "redirect_refused", "no usable Location", "aaaaaaaaaa"],
    ["304 is not a redirect", 304, "http://elsewhere.example/", "unknown", "", ""],
  ];
  for (const [name, status, loc, code, expectIn, expectNot] of cases) {
    it(name, async () => {
      const api = await listen((res) => {
        res.writeHead(status, loc === null ? {} : { Location: loc });
        res.end();
      });
      open.push(api);
      let caught: unknown;
      try {
        await client(api).getBudgetStatus("b");
      } catch (err) {
        caught = err;
      }
      const e = caught as AiplexError;
      expect(e).toBeInstanceOf(AiplexError);
      expect(e.code).toBe(code);
      expect(e.statusCode).toBe(status);
      expect(e.message).toContain(expectIn);
      if (expectNot !== "") {
        expect(String(e)).not.toContain(expectNot);
      }
    });
  }

  it("an opaque redirect (a browser fetch) becomes the typed error", async () => {
    const opaque: typeof fetch = async () => {
      const r = new Response(null, { status: 200 });
      Object.defineProperty(r, "type", { value: "opaqueredirect" });
      Object.defineProperty(r, "status", { value: 0 });
      return r;
    };
    const c = new AiplexClient({ baseUrl: "http://127.0.0.1:1", token: TOKEN, fetch: opaque });
    const err = (await c.getBudgetStatus("b").catch((e: unknown) => e)) as AiplexError;
    expect(err.code).toBe("redirect_refused");
    expect(err.statusCode).toBeNull();
    expect(err.message).toContain("no usable Location");
  });
});

describe("a caller-supplied fetch", () => {
  it("is the one called, and receives redirect manual in the request init", async () => {
    let seen: RequestInit | undefined;
    let calls = 0;
    const custom: typeof fetch = async (_url, init) => {
      calls += 1;
      seen = init;
      return new Response("{}", { status: 200 });
    };
    const c = new AiplexClient({ baseUrl: "http://127.0.0.1:1", token: TOKEN, fetch: custom });
    await c.getBudgetStatus("b").catch(() => undefined);
    expect(calls).toBe(1);
    expect(seen?.redirect).toBe("manual");
  });
});
