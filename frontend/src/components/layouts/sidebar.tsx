import { NavLink } from "react-router-dom"
import {
  Activity,
  BarChart3,
  Building2,
  BookOpen,
  Briefcase,
  FileText,
  Flag,
  Gauge,
  Landmark,
  LineChart,
  LayoutDashboard,
  MessageCircle,
  Newspaper,
  Radar,
  Receipt,
  ScrollText,
  Search,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  TerminalSquare,
  UserCog,
  Users,
  Wallet,
  XCircle,
} from "lucide-react"

import { paths } from "@/config/paths"
import { cn } from "@/lib/utils"
import { useUIStore } from "@/stores/ui"

interface NavItem {
  label: string
  to: string
  icon: React.ComponentType<{ className?: string }>
  group: "overview" | "trading" | "intelligence" | "system" | "help"
}

const nav: NavItem[] = [
  { label: "Overview", to: paths.overview.getHref(), icon: LayoutDashboard, group: "overview" },
  { label: "Analytics", to: paths.analytics.getHref(), icon: LineChart, group: "overview" },
  { label: "Portfolios", to: paths.portfolios.getHref(), icon: Wallet, group: "trading" },
  { label: "Positions", to: paths.positions.getHref(), icon: Briefcase, group: "trading" },
  { label: "Orders", to: paths.orders.getHref(), icon: Receipt, group: "trading" },
  { label: "Cooldowns", to: paths.cooldowns.getHref(), icon: ShieldAlert, group: "trading" },
  { label: "Rejections", to: paths.rejections.getHref(), icon: XCircle, group: "trading" },
  { label: "Risk limits", to: paths.risk.getHref(), icon: ShieldCheck, group: "trading" },
  { label: "Quote lookup", to: paths.quote.getHref(), icon: Search, group: "trading" },
  { label: "Signals", to: paths.signals.getHref(), icon: Radar, group: "intelligence" },
  { label: "Decisions", to: paths.decisions.getHref(), icon: Sparkles, group: "intelligence" },
  { label: "News", to: paths.news.getHref(), icon: Newspaper, group: "intelligence" },
  { label: "Politicians", to: paths.politicians.getHref(), icon: Users, group: "intelligence" },
  { label: "Politician trades", to: paths.politicianTrades.getHref(), icon: Landmark, group: "intelligence" },
  { label: "Insiders", to: paths.insiders.getHref(), icon: UserCog, group: "intelligence" },
  { label: "Social buzz", to: paths.social.getHref(), icon: MessageCircle, group: "intelligence" },
  { label: "Lobbying", to: paths.lobbying.getHref(), icon: Flag, group: "intelligence" },
  { label: "Gov contracts", to: paths.contracts.getHref(), icon: Building2, group: "intelligence" },
  { label: "LLM calls", to: paths.llmCalls.getHref(), icon: TerminalSquare, group: "system" },
  { label: "LLM usage", to: paths.llmUsage.getHref(), icon: BarChart3, group: "system" },
  { label: "Engine", to: paths.engine.getHref(), icon: Gauge, group: "system" },
  { label: "Broker", to: paths.broker.getHref(), icon: LineChart, group: "system" },
  { label: "Audit log", to: paths.audit.getHref(), icon: ScrollText, group: "system" },
  { label: "FAQ & glossary", to: paths.faq.getHref(), icon: BookOpen, group: "help" },
]

const groupOrder: Array<{ id: NavItem["group"]; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "trading", label: "Trading" },
  { id: "intelligence", label: "Intelligence" },
  { id: "system", label: "System" },
  { id: "help", label: "Help" },
]

export function Sidebar() {
  const collapsed = useUIStore((s) => s.sidebarCollapsed)

  return (
    <aside
      className={cn(
        "bg-background sticky top-0 flex h-svh shrink-0 flex-col border-r transition-[width] duration-200",
        collapsed ? "w-[64px]" : "w-[240px]",
      )}
    >
      <div className="flex h-14 items-center gap-2 border-b px-4">
        <div className="bg-primary text-primary-foreground grid size-7 place-items-center rounded-md">
          <Activity className="size-4" />
        </div>
        {!collapsed && (
          <div className="flex flex-col leading-tight">
            <span className="text-sm font-semibold">Trader</span>
            <span className="text-muted-foreground text-[10px] uppercase tracking-wide">
              Dashboard
            </span>
          </div>
        )}
      </div>

      <nav className="flex-1 overflow-y-auto p-2">
        {groupOrder.map((group) => (
          <div key={group.id} className="mb-4">
            {!collapsed && (
              <div className="text-muted-foreground px-2 pb-1 text-[10px] font-medium uppercase tracking-wider">
                {group.label}
              </div>
            )}
            <ul className="flex flex-col gap-0.5">
              {nav
                .filter((n) => n.group === group.id)
                .map((item) => (
                  <li key={item.to}>
                    <NavLink
                      to={item.to}
                      className={({ isActive }) =>
                        cn(
                          "group/nav hover:bg-muted flex h-8 items-center gap-2 rounded-md px-2 text-sm transition-colors",
                          isActive &&
                            "bg-primary/10 text-primary hover:bg-primary/15 font-medium",
                          collapsed && "justify-center px-0",
                        )
                      }
                      title={collapsed ? item.label : undefined}
                    >
                      <item.icon className="size-4 shrink-0" />
                      {!collapsed && <span className="truncate">{item.label}</span>}
                    </NavLink>
                  </li>
                ))}
            </ul>
          </div>
        ))}
      </nav>

      <div className="border-t p-3 text-[10px] text-muted-foreground">
        {!collapsed ? (
          <div className="flex flex-col gap-0.5">
            <span>Trader API dashboard</span>
            <a
              href="https://github.com/alan2207/bulletproof-react"
              target="_blank"
              rel="noreferrer"
              className="hover:text-foreground underline underline-offset-2"
            >
              bulletproof-react
            </a>
          </div>
        ) : (
          <FileText className="size-4" />
        )}
      </div>
    </aside>
  )
}
