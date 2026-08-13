export class ApiError extends Error {
  constructor(
    message: string,
    readonly kind: "http" | "network" | "timeout" | "invalid-response",
    readonly status?: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export type Decoder<T> = (value: unknown) => T;

let unauthorizedHandler: (() => void) | undefined;

export function onUnauthorized(handler?: () => void) {
  unauthorizedHandler = handler;
}

export const unknownResponse: Decoder<unknown> = (value) => value;

export async function api<T>(
  path: string,
  decoder: Decoder<T>,
  init: RequestInit = {},
  timeoutMs = 30_000,
): Promise<T> {
  const timeout = new AbortController();
  const timer = window.setTimeout(() => timeout.abort("timeout"), timeoutMs);
  const signal = init.signal
    ? AbortSignal.any([init.signal, timeout.signal])
    : timeout.signal;
  const headers = new Headers(init.headers);
  headers.set("X-Requested-With", "codex-helper");
  if (init.body != null) headers.set("Content-Type", "application/json");

  try {
    const response = await fetch(`/api/v1/${path}`, {
      credentials: "same-origin",
      ...init,
      headers,
      signal,
    });
    const json: unknown = await response.json().catch(() => undefined);
    if (!response.ok) {
      if (response.status === 401) unauthorizedHandler?.();
      const message =
        isRecord(json) && typeof json.error === "string"
          ? json.error
          : `HTTP ${response.status}`;
      throw new ApiError(message, "http", response.status);
    }
    try {
      return decoder(json);
    } catch (error) {
      throw new ApiError(toErrorMessage(error), "invalid-response");
    }
  } catch (error) {
    if (error instanceof ApiError) throw error;
    if (signal.aborted) {
      if (init.signal?.aborted) throw error;
      throw new ApiError("请求超时，请重试", "timeout");
    }
    throw new ApiError("网络请求失败，请检查连接后重试", "network");
  } finally {
    window.clearTimeout(timer);
  }
}

export const get = <T>(
  path: string,
  decoder: Decoder<T>,
  signal?: AbortSignal,
) => api(path, decoder, { signal });
export const post = <T>(
  path: string,
  decoder: Decoder<T>,
  value: unknown = {},
  signal?: AbortSignal,
  timeoutMs = 30_000,
) =>
  api(
    path,
    decoder,
    { method: "POST", body: JSON.stringify(value), signal },
    timeoutMs,
  );
export const put = <T>(path: string, decoder: Decoder<T>, value: unknown) =>
  api(path, decoder, { method: "PUT", body: JSON.stringify(value) });
export const del = <T>(path: string, decoder: Decoder<T>) =>
  api(path, decoder, { method: "DELETE" });

export function toErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "发生未知错误";
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function record(value: unknown, name = "响应"): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(`${name}格式无效`);
  return value;
}

export function string(value: unknown, name: string): string {
  if (typeof value !== "string") throw new Error(`${name}格式无效`);
  return value;
}

export function number(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isFinite(value))
    throw new Error(`${name}格式无效`);
  return value;
}

export function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${name}格式无效`);
  return value;
}

export function nullableString(value: unknown, name: string): string | null {
  if (value === null) return null;
  return string(value, name);
}
