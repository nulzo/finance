// Environment config. Vite exposes VITE_* variables on import.meta.env.
//
// The frontend talks to the Go trader API at `API_URL`. In development
// Vite proxies /api -> the backend (see vite.config.ts), so leave
// API_URL="/api" unless you want to hit a remote backend directly.

const env = import.meta.env

function str(key: string, fallback = ""): string {
  const v = env[key as keyof typeof env]
  return typeof v === "string" && v.length > 0 ? v : fallback
}

export const config = {
  apiUrl: str("VITE_API_URL", "/api"),
  apiToken: str("VITE_API_TOKEN", ""),
  appName: str("VITE_APP_NAME", "Trader"),
  defaultPortfolioId: str("VITE_DEFAULT_PORTFOLIO_ID", ""),
} as const

export type Config = typeof config
