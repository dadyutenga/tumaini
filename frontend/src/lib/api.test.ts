import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, get, session } from "./api";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => {
  session.setToken(null);
  session.setBranch(null);
  session.onLost(null);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("API client", () => {
  it("sends the token, branch and idempotency headers and unwraps data", async () => {
    const fetchMock = vi.fn().mockResolvedValue(json({ data: { id: "1" } }, 201));
    vi.stubGlobal("fetch", fetchMock);
    session.setToken("abc");
    session.setBranch("branch-1");
    const result = await api.post("/sales", { items: [] }, { idempotencyKey: "key-1" });
    expect(result).toEqual({ id: "1" });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/sales");
    expect(init.credentials).toBe("include");
    expect(init.headers).toMatchObject({
      Authorization: "Bearer abc",
      "X-Branch-Id": "branch-1",
      "Idempotency-Key": "key-1",
      "Content-Type": "application/json",
    });
  });

  it("builds query strings and skips empty values", async () => {
    const fetchMock = vi.fn().mockResolvedValue(json({ data: [] }));
    vi.stubGlobal("fetch", fetchMock);
    await get("/products", { q: "sugar", page: 2, low_stock: undefined, category_id: "" });
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/products?q=sugar&page=2");
  });

  it("turns the error envelope into an ApiError with field messages", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        json(
          {
            error: {
              code: "invalid_body",
              message: "Check the form",
              fields: { name: "Required" },
            },
          },
          400,
        ),
      ),
    );
    await expect(api.post("/products", {})).rejects.toMatchObject({
      name: "ApiError",
      status: 400,
      code: "invalid_body",
      message: "Check the form",
      fields: { name: "Required" },
    });
  });

  it("refreshes the session once on 401 and retries the request", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ error: { code: "invalid_token", message: "expired" } }, 401))
      .mockResolvedValueOnce(json({ data: { access_token: "fresh" } }))
      .mockResolvedValueOnce(json({ data: { ok: true } }));
    vi.stubGlobal("fetch", fetchMock);
    session.setToken("stale");
    await expect(get("/me")).resolves.toEqual({ ok: true });
    expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/auth/refresh");
    expect((fetchMock.mock.calls[2][1] as RequestInit).headers).toMatchObject({
      Authorization: "Bearer fresh",
    });
  });

  it("signs the user out when the refresh fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          json({ error: { code: "session_ended", message: "Sign in again" } }, 401),
        ),
    );
    const lost = vi.fn();
    session.onLost(lost);
    await expect(get("/me")).rejects.toBeInstanceOf(ApiError);
    expect(lost).toHaveBeenCalledOnce();
  });

  it("reports network failures as status 0", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    await expect(get("/me")).rejects.toMatchObject({ status: 0, code: "network" });
  });
});
