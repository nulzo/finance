import { useEffect, useMemo } from "react"

import { usePortfolios } from "@/features/portfolios/api"
import { useUIStore } from "@/stores/ui"

/** Resolves the currently-selected portfolio. Falls back to the first
 *  portfolio returned by the API and persists the selection in the UI
 *  store so subsequent routes stay in sync. */
export function useCurrentPortfolio() {
  const portfolios = usePortfolios()
  const portfolioId = useUIStore((s) => s.portfolioId)
  const setPortfolioId = useUIStore((s) => s.setPortfolioId)

  const selected = useMemo(() => {
    if (!portfolios.data?.length) return undefined
    return portfolios.data.find((p) => p.id === portfolioId) ?? portfolios.data[0]
  }, [portfolios.data, portfolioId])

  useEffect(() => {
    if (!portfolioId && selected) {
      setPortfolioId(selected.id)
    }
  }, [portfolioId, selected, setPortfolioId])

  return {
    portfolios: portfolios.data ?? [],
    portfolio: selected,
    portfolioId: selected?.id,
    isLoading: portfolios.isLoading,
    error: portfolios.error,
    setPortfolioId,
  }
}
