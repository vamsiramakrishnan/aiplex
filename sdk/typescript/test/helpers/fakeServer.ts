import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";

export interface FakeResponse {
  status?: number;
  json?: unknown;
  text?: string;
  headers?: Record<string, string>;
}

export interface RecordedRequest {
  method: string;
  path: string;
  query: Record<string, string>;
  rawSearch: string;
  headers: Record<string, string>;
  body: unknown;
}

export interface FakeApi {
  url: string;
  route: (method: string, path: string, ...responses: FakeResponse[]) => void;
  requests: RecordedRequest[];
  close: () => Promise<void>;
}

export async function startFakeApi(): Promise<FakeApi> {
  const routes = new Map<string, FakeResponse[]>();
  const requests: RecordedRequest[] = [];

  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => chunks.push(chunk));
    req.on("end", () => {
      const rawBody = Buffer.concat(chunks).toString("utf8");
      let parsedBody: unknown = null;
      if (rawBody.length > 0) {
        try {
          parsedBody = JSON.parse(rawBody) as unknown;
        } catch {
          parsedBody = rawBody;
        }
      }

      const method = (req.method ?? "GET").toUpperCase();
      const parsedUrl = new URL(req.url ?? "/", "http://127.0.0.1");
      const query: Record<string, string> = {};
      for (const [k, v] of parsedUrl.searchParams.entries()) {
        if (!(k in query)) {
          query[k] = v;
        }
      }

      const headers: Record<string, string> = {};
      for (const [k, v] of Object.entries(req.headers)) {
        if (typeof v === "string") {
          headers[k.toLowerCase()] = v;
        } else if (Array.isArray(v)) {
          headers[k.toLowerCase()] = v.join(", ");
        }
      }

      requests.push({
        method,
        path: parsedUrl.pathname,
        query,
        rawSearch: parsedUrl.search,
        headers,
        body: parsedBody,
      });

      const key = `${method} ${parsedUrl.pathname}`;
      const queue = routes.get(key);
      if (!queue || queue.length === 0) {
        res.writeHead(404, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ code: "NOT_FOUND", message: "no fake route" }));
        return;
      }

      const item = queue.length > 1 ? queue.shift()! : queue[0]!;
      const status = item.status ?? 200;
      const outHeaders: Record<string, string> = { ...item.headers };

      if (status === 204) {
        res.writeHead(204, outHeaders);
        res.end();
        return;
      }

      if (item.text !== undefined) {
        if (!outHeaders["Content-Type"] && !outHeaders["content-type"]) {
          outHeaders["Content-Type"] = "text/plain";
        }
        res.writeHead(status, outHeaders);
        res.end(item.text);
        return;
      }

      if (!outHeaders["Content-Type"] && !outHeaders["content-type"]) {
        outHeaders["Content-Type"] = "application/json";
      }
      res.writeHead(status, outHeaders);
      res.end(JSON.stringify(item.json ?? {}));
    });
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });

  const addr = server.address() as AddressInfo;
  const url = `http://127.0.0.1:${addr.port}`;

  return {
    url,
    route(method: string, path: string, ...responses: FakeResponse[]): void {
      routes.set(`${method.toUpperCase()} ${path}`, [...responses]);
    },
    requests,
    close(): Promise<void> {
      return new Promise<void>((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      });
    },
  };
}
