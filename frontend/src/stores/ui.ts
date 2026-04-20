import { create } from "zustand"
import { persist } from "zustand/middleware"

import { config } from "@/config/env"

interface UIState {
  sidebarCollapsed: boolean
  toggleSidebar: () => void
  setSidebar: (v: boolean) => void
  portfolioId: string
  setPortfolioId: (id: string) => void
}

export const useUIStore = create<UIState>()(
  persist(
    (set, get) => ({
      sidebarCollapsed: false,
      toggleSidebar: () => set({ sidebarCollapsed: !get().sidebarCollapsed }),
      setSidebar: (v) => set({ sidebarCollapsed: v }),
      portfolioId: config.defaultPortfolioId,
      setPortfolioId: (id) => set({ portfolioId: id }),
    }),
    { name: "trader.ui" },
  ),
)
