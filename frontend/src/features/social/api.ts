import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api-client"
import type { SocialPost } from "@/types/api"

export type SocialPlatform = "" | "wsb" | "twitter"

export const socialKeys = {
  all: ["social"] as const,
  list: (
    sym: string,
    platform: string,
    since: string,
    limit: number,
  ) => [...socialKeys.all, sym, platform, since, limit] as const,
}

export interface UseSocialOptions {
  symbol?: string
  /** "" == all platforms; "wsb" / "twitter" filter server-side. */
  platform?: SocialPlatform
  since?: string
  limit?: number
}

/** WSB + Twitter mention rollups. Polls every 60 s to stay fresh
 *  while the engine ingestion runs on its own timer. */
export function useSocial(opts: UseSocialOptions = {}) {
  const symbol = (opts.symbol ?? "").toUpperCase().trim()
  const platform = opts.platform ?? ""
  const since = opts.since ?? "48h"
  const limit = opts.limit ?? 500
  return useQuery({
    queryKey: socialKeys.list(symbol, platform, since, limit),
    refetchInterval: 60_000,
    queryFn: async () => {
      const data = await api.get<SocialPost[]>("/v1/social", {
        params: {
          symbol: symbol || undefined,
          platform: platform || undefined,
          since,
          limit,
        },
      })
      return data ?? []
    },
  })
}
