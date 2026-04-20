import { Wallet } from "lucide-react"

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useCurrentPortfolio } from "@/hooks/use-current-portfolio"

export function PortfolioSwitcher() {
  const { portfolios, portfolioId, setPortfolioId } = useCurrentPortfolio()

  if (!portfolios.length) return null

  return (
    <Select value={portfolioId} onValueChange={setPortfolioId}>
      <SelectTrigger size="sm" className="min-w-[180px]">
        <Wallet className="size-3.5" />
        <SelectValue placeholder="Select portfolio" />
      </SelectTrigger>
      <SelectContent>
        {portfolios.map((p) => (
          <SelectItem key={p.id} value={p.id}>
            <span className="font-medium">{p.name}</span>
            <span className="text-muted-foreground ml-1 text-xs">
              ({p.mode})
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
