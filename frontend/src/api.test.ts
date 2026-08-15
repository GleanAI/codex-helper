import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getEventually, post, unknownResponse } from "./api";

class TestDocument extends EventTarget {
  visibilityState: DocumentVisibilityState = "visible";
}

describe("后台读取重试", () => {
  let testDocument: TestDocument;
  let testWindow: EventTarget & Pick<Window, "setTimeout" | "clearTimeout">;
  let online: boolean;

  beforeEach(() => {
    vi.useFakeTimers();
    online = true;
    testDocument = new TestDocument();
    testWindow = Object.assign(new EventTarget(), {
      setTimeout: globalThis.setTimeout,
      clearTimeout: globalThis.clearTimeout,
    });
    vi.stubGlobal("window", testWindow);
    vi.stubGlobal("document", testDocument);
    vi.stubGlobal("navigator", {
      get onLine() {
        return online;
      },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("网络失败后按退避间隔重试并返回成功响应", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError("offline"))
      .mockResolvedValueOnce(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const result = getEventually("status", unknownResponse);
    await vi.advanceTimersByTimeAsync(999);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);

    await expect(result).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("离线时暂停并在 online 事件后立即重试", async () => {
    online = false;
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const result = getEventually("status", unknownResponse);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMock).not.toHaveBeenCalled();

    online = true;
    testWindow.dispatchEvent(new Event("online"));
    await vi.advanceTimersByTimeAsync(0);

    await expect(result).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("请求进行中进入后台时取消，并在恢复前台后重新读取", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(
        (_input, init) =>
          new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener("abort", () =>
              reject(new DOMException("aborted", "AbortError")),
            );
          }),
      )
      .mockResolvedValueOnce(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    const result = getEventually("status", unknownResponse);
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    testDocument.visibilityState = "hidden";
    testDocument.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    testDocument.visibilityState = "visible";
    testDocument.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(0);

    await expect(result).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("调用方取消后停止等待和重试", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new TypeError("offline"));
    vi.stubGlobal("fetch", fetchMock);
    const controller = new AbortController();

    const result = getEventually("status", unknownResponse, controller.signal);
    await vi.advanceTimersByTimeAsync(0);
    controller.abort();

    await expect(result).rejects.toMatchObject({ name: "AbortError" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("非瞬时 HTTP 错误不重试", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        Response.json({ error: "bad request" }, { status: 400 }),
      );
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      getEventually("status", unknownResponse),
    ).rejects.toMatchObject({ kind: "http", status: 400 });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("用户主动写请求失败时不自动重放", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new TypeError("offline"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(post("save", unknownResponse)).rejects.toMatchObject({
      kind: "network",
    });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
