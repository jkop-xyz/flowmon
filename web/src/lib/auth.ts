import { ApiError, postJSON } from "@/lib/api"

export type User = { username: string; isAdmin: boolean }

export type SetupStep = "database" | "admin" | "done"

export type SetupStatus = {
  step: SetupStep
  databaseLocked: boolean
  database?: string
  databaseUnavailable: boolean
  sslModes: string[]
}

export type DatabaseChoice =
  | { driver: "sqlite" }
  | {
      driver: "postgres"
      host: string
      port: number
      database: string
      user: string
      password: string
      schema: string
      sslMode: string
    }

export async function fetchSetupStatus(signal?: AbortSignal): Promise<SetupStatus> {
  const response = await fetch("/api/setup/status", { signal })
  if (!response.ok) throw new ApiError(response.status, "the App Store isn't available")
  return (await response.json()) as SetupStatus
}

/** The current user, or null when not logged in. */
export async function fetchSession(signal?: AbortSignal): Promise<User | null> {
  const response = await fetch("/api/auth/session", { signal })
  if (response.status === 401) return null
  if (!response.ok) throw new ApiError(response.status, "the App Store isn't available")
  return ((await response.json()) as { user: User }).user
}

export const login = (username: string, password: string) =>
  postJSON<{ user: User }>("/api/auth/login", { username, password })

export const logout = () => postJSON<{ ok: boolean }>("/api/auth/logout", {})

export const testDatabase = (token: string, database: DatabaseChoice) =>
  postJSON<{ ok: boolean }>("/api/setup/database/test", { token, database })

export const configureDatabase = (token: string, database: DatabaseChoice) =>
  postJSON<{ ok: boolean }>("/api/setup/database", { token, database })

export const createAdmin = (token: string, username: string, password: string) =>
  postJSON<{ ok: boolean }>("/api/setup/admin", { token, username, password })
