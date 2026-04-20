import { QueryClient } from "@tanstack/react-query"
import type { DefaultOptions } from "@tanstack/react-query"

const defaultOptions: DefaultOptions = {
  queries: {
    refetchOnWindowFocus: false,
    retry: (failureCount, error: unknown) => {
      const status = (error as { status?: number } | null)?.status ?? 0
      if (status === 401 || status === 403 || status === 404) return false
      return failureCount < 2
    },
    staleTime: 30_000,
  },
}

export const queryClient = new QueryClient({ defaultOptions })
