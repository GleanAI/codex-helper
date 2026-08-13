import {
  boolean,
  nullableString,
  number,
  record,
  string,
  type Decoder,
} from "./api";

export type Theme = "system" | "dark" | "light";
export type ExpectedKind = "any" | "personal" | "team";
export type ValidationStatus = "pending" | "matched" | "mismatch" | "unknown";
export type AsyncState<T> =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "success"; data: T };

export interface Status {
  initialized: boolean;
  appServer: boolean;
  version: string;
}
export interface Account {
  id: number;
  displayName: string;
  email: string | null;
  planType: string | null;
  expectedKind: ExpectedKind;
  actualKind: "unknown" | "personal" | "team";
  validationStatus: ValidationStatus;
  possibleDuplicate: boolean;
  connected: boolean;
}
export interface DeviceLogin {
  accountId: number;
  verificationUrl: string;
  userCode: string;
}
export interface Limit {
  limitId: string;
  limitName: string | null;
  windowType: string;
  usedPercent: number;
  windowDurationMinutes: number;
  resetsAt: number;
}
export interface Point {
  date: string;
  totalTokens: number;
}
export interface Dashboard {
  accountId: number;
  displayName: string;
  account: {
    email: string | null;
    planType: string | null;
    connected: boolean;
  };
  limits: Limit[];
  summary: {
    lifetimeTokens?: number | null;
    peakDailyTokens?: number | null;
    currentStreakDays?: number | null;
    longestRunningTurnSec?: number | null;
  };
  usage: Point[];
  fetchedAt: number;
  stale: boolean;
  lastError?: string;
}
export interface GeneralSettings {
  timezone: string;
  theme: Theme;
  syncMinutes: number;
  retentionDays: number;
  beforeMinutes: number;
  notifyBefore: boolean;
  notifyAfter: boolean;
}
export interface TelegramSettingsResponse {
  chatId: number;
  enabled: boolean;
  menuEnabled: boolean;
  configured: boolean;
  botName?: string;
  warning?: string;
}
export type TelegramSettingsForm = TelegramSettingsResponse & { token: string };
export interface ActionResponse {
  ok: boolean;
  warning?: string;
}
export interface SMTPSettingsResponse {
  host: string;
  port: number;
  username: string;
  from: string;
  fromName: string;
  to: string;
  security: "starttls" | "tls" | "none";
  enabled: boolean;
  configured: boolean;
}
export type SMTPSettingsForm = SMTPSettingsResponse & { password: string };

const enumValue = <T extends string>(
  value: unknown,
  values: readonly T[],
  name: string,
): T => {
  const result = string(value, name);
  if (!values.includes(result as T)) throw new Error(`${name}格式无效`);
  return result as T;
};
const optionalNumber = (value: unknown, name: string) =>
  value == null ? value : number(value, name);

export const decodeStatus: Decoder<Status> = (value) => {
  const x = record(value);
  return {
    initialized: boolean(x.initialized, "initialized"),
    appServer: boolean(x.appServer, "appServer"),
    version: string(x.version, "version"),
  };
};
export const decodeAccount: Decoder<Account> = (value) => {
  const x = record(value, "账号");
  return {
    id: number(x.id, "id"),
    displayName: string(x.displayName, "displayName"),
    email: nullableString(x.email, "email"),
    planType: nullableString(x.planType, "planType"),
    expectedKind: enumValue(
      x.expectedKind,
      ["any", "personal", "team"],
      "expectedKind",
    ),
    actualKind: enumValue(
      x.actualKind,
      ["unknown", "personal", "team"],
      "actualKind",
    ),
    validationStatus: enumValue(
      x.validationStatus,
      ["pending", "matched", "mismatch", "unknown"],
      "validationStatus",
    ),
    possibleDuplicate: boolean(x.possibleDuplicate, "possibleDuplicate"),
    connected: boolean(x.connected, "connected"),
  };
};
export const decodeAccounts: Decoder<Account[]> = (value) => {
  if (!Array.isArray(value)) throw new Error("账号列表格式无效");
  return value.map(decodeAccount);
};
export const decodeDashboard: Decoder<Dashboard> = (value) => {
  const x = record(value, "Dashboard"),
    account = record(x.account, "account"),
    summary = record(x.summary, "summary");
  if (!Array.isArray(x.limits) || !Array.isArray(x.usage))
    throw new Error("Dashboard列表格式无效");
  return {
    accountId: number(x.accountId, "accountId"),
    displayName: string(x.displayName, "displayName"),
    account: {
      email: nullableString(account.email, "email"),
      planType: nullableString(account.planType, "planType"),
      connected: boolean(account.connected, "connected"),
    },
    limits: x.limits.map((v) => {
      const l = record(v, "limit");
      return {
        limitId: string(l.limitId, "limitId"),
        limitName: nullableString(l.limitName, "limitName"),
        windowType: string(l.windowType, "windowType"),
        usedPercent: number(l.usedPercent, "usedPercent"),
        windowDurationMinutes: number(
          l.windowDurationMinutes,
          "windowDurationMinutes",
        ),
        resetsAt: number(l.resetsAt, "resetsAt"),
      };
    }),
    summary: {
      lifetimeTokens: optionalNumber(summary.lifetimeTokens, "lifetimeTokens"),
      peakDailyTokens: optionalNumber(
        summary.peakDailyTokens,
        "peakDailyTokens",
      ),
      currentStreakDays: optionalNumber(
        summary.currentStreakDays,
        "currentStreakDays",
      ),
      longestRunningTurnSec: optionalNumber(
        summary.longestRunningTurnSec,
        "longestRunningTurnSec",
      ),
    },
    usage: x.usage.map((v) => {
      const p = record(v, "usage");
      return {
        date: string(p.date, "date"),
        totalTokens: number(p.totalTokens, "totalTokens"),
      };
    }),
    fetchedAt: number(x.fetchedAt, "fetchedAt"),
    stale: boolean(x.stale, "stale"),
    ...(typeof x.lastError === "string" ? { lastError: x.lastError } : {}),
  };
};
export const decodeGeneral: Decoder<GeneralSettings> = (value) => {
  const x = record(value);
  return {
    timezone: string(x.timezone, "timezone"),
    theme: enumValue(x.theme, ["system", "dark", "light"], "theme"),
    syncMinutes: number(x.syncMinutes, "syncMinutes"),
    retentionDays: number(x.retentionDays, "retentionDays"),
    beforeMinutes: number(x.beforeMinutes, "beforeMinutes"),
    notifyBefore: boolean(x.notifyBefore, "notifyBefore"),
    notifyAfter: boolean(x.notifyAfter, "notifyAfter"),
  };
};
export const decodeTelegram: Decoder<TelegramSettingsResponse> = (value) => {
  const x = record(value);
  return {
    chatId: number(x.chatId ?? 0, "chatId"),
    enabled: boolean(x.enabled, "enabled"),
    menuEnabled: boolean(x.menuEnabled, "menuEnabled"),
    configured: boolean(x.configured, "configured"),
    ...(typeof x.botName === "string" ? { botName: x.botName } : {}),
    ...(typeof x.warning === "string" ? { warning: x.warning } : {}),
  };
};
export const decodeAction: Decoder<ActionResponse> = (value) => {
  const x = record(value);
  return {
    ok: boolean(x.ok, "ok"),
    ...(typeof x.warning === "string" ? { warning: x.warning } : {}),
  };
};
export const decodeSMTP: Decoder<SMTPSettingsResponse> = (value) => {
  const x = record(value);
  return {
    host: string(x.host, "host"),
    port: number(x.port, "port"),
    username: string(x.username, "username"),
    from: string(x.from, "from"),
    fromName: string(x.fromName, "fromName"),
    to: string(x.to, "to"),
    security: enumValue(x.security, ["starttls", "tls", "none"], "security"),
    enabled: boolean(x.enabled, "enabled"),
    configured: boolean(x.configured, "configured"),
  };
};
export const decodeDeviceLogin: Decoder<Omit<DeviceLogin, "accountId">> = (
  value,
) => {
  const x = record(value),
    verificationUrl = string(x.verificationUrl, "verificationUrl");
  let url: URL;
  try {
    url = new URL(verificationUrl);
  } catch {
    throw new Error("设备授权地址无效");
  }
  if (url.protocol !== "https:") throw new Error("设备授权地址必须使用 HTTPS");
  return { verificationUrl, userCode: string(x.userCode, "userCode") };
};
export const decodeOK: Decoder<{ ok: boolean }> = (value) => {
  const x = record(value);
  return { ok: boolean(x.ok, "ok") };
};
export const decodeCode: Decoder<{ code: string }> = (value) => {
  const x = record(value);
  return { code: string(x.code, "code") };
};
