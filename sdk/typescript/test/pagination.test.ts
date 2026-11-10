import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { AiplexClient, AiplexError } from "../src/index.js";
import { startFakeApi, type FakeApi } from "./helpers/fakeServer.js";

describe("Pagination iterators", () => {
  let api: FakeApi;
  let client: AiplexClient;

  beforeEach(async () => {
    api = await startFakeApi();
    client = new AiplexClient({ baseUrl: api.url, token: "test-token" });
  });

  afterEach(async () => {
    await api.close();
  });

  it("iterUsageRecords follows nextCursor across three pages and stops", async () => {
    api.route(
      "GET",
      "/api/v1/usage/records",
      { json: { records: [{ id: "ul_1" }, { id: "ul_2" }], next_cursor: "cur_1" } },
      { json: { records: [{ id: "ul_3" }], next_cursor: "cur_2" } },
      { json: { records: [{ id: "ul_4" }] } },
    );

    const ids: string[] = [];
    for await (const rec of client.iterUsageRecords({ plane: "llmplex" }, { limit: 2 })) {
      ids.push(rec.id);
    }

    expect(ids).toEqual(["ul_1", "ul_2", "ul_3", "ul_4"]);
    expect(api.requests).toHaveLength(3);
    expect(api.requests[0]!.query).toEqual({ plane: "llmplex", limit: "2" });
    expect(api.requests[1]!.query).toEqual({
      plane: "llmplex",
      limit: "2",
      cursor: "cur_1",
    });
    expect(api.requests[2]!.query).toEqual({
      plane: "llmplex",
      limit: "2",
      cursor: "cur_2",
    });
  });

  it("throws pagination_stalled when server repeats the same cursor", async () => {
    api.route(
      "GET",
      "/api/v1/usage/records",
      { json: { records: [{ id: "ul_1" }], next_cursor: "same_cursor" } },
      { json: { records: [{ id: "ul_2" }], next_cursor: "same_cursor" } },
    );

    const collect = async (): Promise<void> => {
      for await (const _ of client.iterUsageRecords()) {
        // consume
      }
    };

    await expect(collect()).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(AiplexError);
      const ae = err as AiplexError;
      expect(ae.code).toBe("pagination_stalled");
      expect(ae.statusCode).toBeNull();
      return true;
    });
  });

  it("iterOverviewSubjects and iterOverviewAccessPoints follow pages and handle empty first page", async () => {
    api.route(
      "GET",
      "/api/v1/overview/subjects",
      {
        json: {
          kind: "user",
          subjects: [{ kind: "user", id: "u1" }],
          next_cursor: "s_cur",
        },
      },
      {
        json: {
          kind: "user",
          subjects: [{ kind: "user", id: "u2" }],
        },
      },
    );

    const subjIds: string[] = [];
    for await (const s of client.iterOverviewSubjects("user", { limit: 1 })) {
      subjIds.push(s.id);
    }
    expect(subjIds).toEqual(["u1", "u2"]);

    api.route(
      "GET",
      "/api/v1/overview/access-points",
      {
        json: {
          access_points: [{ id: "ap1" }],
          next_cursor: "ap_cur",
        },
      },
      {
        json: {
          access_points: [{ id: "ap2" }],
        },
      },
    );

    const apIds: string[] = [];
    for await (const ap of client.iterOverviewAccessPoints({ limit: 1 })) {
      apIds.push(ap.id);
    }
    expect(apIds).toEqual(["ap1", "ap2"]);

    api.route("GET", "/api/v1/usage/records", { json: { records: [] } });
    const empty: string[] = [];
    for await (const r of client.iterUsageRecords()) {
      empty.push(r.id);
    }
    expect(empty).toEqual([]);
  });
});
