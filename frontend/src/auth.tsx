import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import {
  ApiError,
  getEventually,
  onUnauthorized,
  post,
  toErrorMessage,
} from "./api";
import { decodeOK, decodeStatus, type AsyncState, type Status } from "./types";

type AuthContextValue = {
  status: Status;
  authenticated: boolean;
  login: (username: string, password: string) => Promise<void>;
  setup: (
    username: string,
    password: string,
    timezone: string,
  ) => Promise<void>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<
    AsyncState<{ status: Status; authenticated: boolean }>
  >({ status: "loading" });
  const load = async () => {
    setState({ status: "loading" });
    try {
      const status = await getEventually("system/status", decodeStatus);
      if (!status.initialized)
        return setState({
          status: "success",
          data: { status, authenticated: false },
        });
      try {
        await getEventually("auth/me", () => undefined);
        setState({ status: "success", data: { status, authenticated: true } });
      } catch (error) {
        if (error instanceof ApiError && error.status === 401)
          setState({
            status: "success",
            data: { status, authenticated: false },
          });
        else throw error;
      }
    } catch (error) {
      setState({ status: "error", message: toErrorMessage(error) });
    }
  };
  useEffect(() => {
    void load();
  }, []);
  useEffect(() => {
    onUnauthorized(() =>
      setState((current) =>
        current.status === "success"
          ? {
              status: "success",
              data: { ...current.data, authenticated: false },
            }
          : current,
      ),
    );
    return () => onUnauthorized();
  }, []);
  if (state.status === "loading")
    return (
      <main className="center">
        <div className="spinner" />
      </main>
    );
  if (state.status === "error")
    return (
      <main className="center">
        <section className="panel form">
          <h2>无法启动应用</h2>
          <p className="error">{state.message}</p>
          <button onClick={() => void load()}>重试</button>
        </section>
      </main>
    );
  const { status, authenticated } = state.data;
  const value: AuthContextValue = {
    status,
    authenticated,
    login: async (username, password) => {
      await post("auth/login", decodeOK, { username, password });
      setState({ status: "success", data: { status, authenticated: true } });
    },
    setup: async (username, password, timezone) => {
      await post("setup", decodeOK, { username, password, timezone });
      setState({
        status: "success",
        data: { status: { ...status, initialized: true }, authenticated: true },
      });
    },
    logout: async () => {
      await post("auth/logout", decodeOK);
      setState({ status: "success", data: { status, authenticated: false } });
    },
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth 必须在 AuthProvider 内使用");
  return value;
}
