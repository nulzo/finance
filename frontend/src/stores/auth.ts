import { create } from "zustand"
import { persist } from "zustand/middleware"

import { config } from "@/config/env"

interface AuthState {
  token: string
  setToken: (t: string) => void
  clearToken: () => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: config.apiToken,
      setToken: (t) => set({ token: t }),
      clearToken: () => set({ token: "" }),
    }),
    { name: "trader.auth" },
  ),
)
