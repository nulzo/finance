import { Navigate, Route, Routes } from "react-router-dom"

import { DashboardLayout } from "@/components/layouts/dashboard-layout"
import { paths } from "@/config/paths"
import { AnalyticsRoute } from "@/features/analytics/route"
import { AuditRoute } from "@/features/audit/route"
import { BrokerRoute } from "@/features/broker/route"
import { ContractsRoute } from "@/features/contracts/route"
import { CooldownsRoute } from "@/features/cooldowns/route"
import { DecisionsRoute } from "@/features/decisions/route"
import { EngineRoute } from "@/features/engine/route"
import { FaqRoute } from "@/features/faq/route"
import { InsidersRoute } from "@/features/insiders/route"
import { LLMCallsRoute, LLMUsageRoute } from "@/features/llm/routes"
import { LobbyingRoute } from "@/features/lobbying/route"
import { NewsRoute } from "@/features/news/route"
import { OrdersRoute } from "@/features/orders/route"
import { OverviewRoute } from "@/features/overview/route"
import { PoliticianTradesRoute } from "@/features/politician-trades/route"
import { PoliticiansRoute } from "@/features/politicians/route"
import { PortfolioDetailRoute, PortfoliosRoute } from "@/features/portfolios/routes"
import { PositionsRoute } from "@/features/positions/route"
import { QuoteRoute } from "@/features/quote/route"
import { RejectionsRoute } from "@/features/rejections/route"
import { RiskRoute } from "@/features/risk/route"
import { SettingsRoute } from "@/features/settings/route"
import { SignalsRoute } from "@/features/signals/route"
import { SocialRoute } from "@/features/social/route"

export function AppRouter() {
  return (
    <Routes>
      <Route element={<DashboardLayout />}>
        <Route path={paths.home.path} element={<Navigate to={paths.overview.path} replace />} />
        <Route path={paths.overview.path} element={<OverviewRoute />} />
        <Route path={paths.analytics.path} element={<AnalyticsRoute />} />
        <Route path={paths.portfolios.path} element={<PortfoliosRoute />} />
        <Route path={paths.portfolio.path} element={<PortfolioDetailRoute />} />
        <Route path={paths.positions.path} element={<PositionsRoute />} />
        <Route path={paths.orders.path} element={<OrdersRoute />} />
        <Route path={paths.cooldowns.path} element={<CooldownsRoute />} />
        <Route path={paths.rejections.path} element={<RejectionsRoute />} />
        <Route path={paths.risk.path} element={<RiskRoute />} />
        <Route path={paths.news.path} element={<NewsRoute />} />
        <Route path={paths.signals.path} element={<SignalsRoute />} />
        <Route path={paths.decisions.path} element={<DecisionsRoute />} />
        <Route path={paths.politicians.path} element={<PoliticiansRoute />} />
        <Route path={paths.politicianTrades.path} element={<PoliticianTradesRoute />} />
        <Route path={paths.insiders.path} element={<InsidersRoute />} />
        <Route path={paths.social.path} element={<SocialRoute />} />
        <Route path={paths.lobbying.path} element={<LobbyingRoute />} />
        <Route path={paths.contracts.path} element={<ContractsRoute />} />
        <Route path={paths.audit.path} element={<AuditRoute />} />
        <Route path={paths.llmCalls.path} element={<LLMCallsRoute />} />
        <Route path={paths.llmUsage.path} element={<LLMUsageRoute />} />
        <Route path={paths.engine.path} element={<EngineRoute />} />
        <Route path={paths.broker.path} element={<BrokerRoute />} />
        <Route path={paths.quote.path} element={<QuoteRoute />} />
        <Route path={paths.settings.path} element={<SettingsRoute />} />
        <Route path={paths.faq.path} element={<FaqRoute />} />
        <Route path="*" element={<Navigate to={paths.overview.path} replace />} />
      </Route>
    </Routes>
  )
}
