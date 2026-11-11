import { useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import {
  Globe,
  Radio,
  Network as NetworkIcon,
  Users,
  Activity,
  Sun,
  Moon,
  ShieldCheck,
  AlertCircle,
  ExternalLink,
  GitFork,
  AlertTriangle,
  LogOut,
  UserCheck,
} from 'lucide-react'

import { useSyncStatus } from './api/queries'
import { useAuth } from './components/AuthGate'
import { useTelemetryWebSocket } from './hooks/useTelemetryWebSocket'
import { LiveTicker } from './components/LiveTicker'
import ClientsPage from './pages/ClientsPage'
import DevicesPage from './pages/DevicesPage'
import NetworksPage from './pages/NetworksPage'
import SitesPage from './pages/SitesPage'
import TopologyPage from './pages/TopologyPage'

export default function App() {
  const [darkMode, setDarkMode] = useState<boolean>(() => {
    return (
      localStorage.getItem('theme') === 'dark' ||
      (!localStorage.getItem('theme') &&
        window.matchMedia('(prefers-color-scheme: dark)').matches)
    )
  })

  useEffect(() => {
    if (darkMode) {
      document.documentElement.classList.add('dark')
      localStorage.setItem('theme', 'dark')
    } else {
      document.documentElement.classList.remove('dark')
      localStorage.setItem('theme', 'light')
    }
  }, [darkMode])

  const { user, openLoginModal, logout } = useAuth()
  const syncStatus = useSyncStatus()
  const { isConnected, payload, latencyHistory, lossHistory, activeAlert, clearAlert } =
    useTelemetryWebSocket()
  const isHealthy = syncStatus.data && !syncStatus.data.stale && syncStatus.data.unifi?.reachable
  const location = useLocation()
  function getPageTitle(path: string) {
    if (path.startsWith('/devices')) return 'Devices & Hardware'
    if (path.startsWith('/sites')) return 'Sites & Locations'
    if (path.startsWith('/networks')) return 'Networks & VLANs'
    if (path.startsWith('/clients')) return 'Connected Clients'
    if (path.startsWith('/topology')) return 'Network Topology Graph'
    return 'Dashboard'
  }

  return (
    <div className="min-h-screen flex bg-background text-foreground">
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      {/* Enterprise Sidebar */}
      <aside className="w-64 border-r border-border bg-card flex flex-col justify-between flex-shrink-0">
        <div>
          {/* Brand header */}
          <div className="p-5 border-b border-border flex items-center gap-3">
            <div className="h-9 w-9 rounded-lg bg-primary flex items-center justify-center text-primary-foreground shadow-sm">
              <Activity className="h-5 w-5" />
            </div>
            <div>
              <h1 className="text-sm font-bold tracking-tight text-foreground">
                Network Orchestrator
              </h1>
              <p className="text-[11px] font-medium text-muted-foreground flex items-center gap-1.5 mt-0.5">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                UniFi Control Plane
              </p>
            </div>
          </div>

          {/* Navigation links */}
          <nav aria-label="Primary" className="p-3 space-y-1">
            <NavLink
              to="/sites"
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`
              }
            >
              <Globe className="h-4 w-4" />
              <span>Sites</span>
            </NavLink>

            <NavLink
              to="/devices"
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`
              }
            >
              <Radio className="h-4 w-4" />
              <span>Devices</span>
            </NavLink>

            <NavLink
              to="/clients"
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`
              }
            >
              <Users className="h-4 w-4" />
              <span>Clients</span>
            </NavLink>

            <NavLink
              to="/networks"
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`
              }
            >
              <NetworkIcon className="h-4 w-4" />
              <span>Networks</span>
            </NavLink>

            <NavLink
              to="/topology"
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`
              }
            >
              <GitFork className="h-4 w-4" />
              <span>Topology</span>
            </NavLink>
          </nav>
        </div>

        {/* Sidebar Footer: Telemetry & Theme */}
        <div className="p-3 border-t border-border space-y-3">
          {/* Controller Sync Status Box */}
          <div className="rounded-lg border border-border bg-muted/40 p-3 text-xs">
            <div className="flex items-center justify-between font-semibold text-foreground">
              <span className="flex items-center gap-1.5">
                {isHealthy ? (
                  <ShieldCheck className="h-3.5 w-3.5 text-emerald-500" />
                ) : (
                  <AlertCircle className="h-3.5 w-3.5 text-amber-500" />
                )}
                UniFi Cloud
              </span>
              <span
                className={`text-[10px] font-bold uppercase px-1.5 py-0.5 rounded ${
                  isHealthy
                    ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400'
                    : 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-400'
                }`}
              >
                {syncStatus.data?.unifi?.surface ?? 'cloud'}
              </span>
            </div>

            <div className="mt-2 text-[11px] text-muted-foreground space-y-0.5">
              <p>
                Circuit:{' '}
                <span className="font-medium text-foreground">
                  {syncStatus.data?.circuit_breaker?.state ?? 'closed'}
                </span>
              </p>
              <p>
                Status:{' '}
                <span className="font-medium text-foreground">
                  {syncStatus.data?.stale ? 'Stale' : 'Synchronized'}
                </span>
              </p>
            </div>
          </div>

          {/* Operator Identity & RBAC Session Box */}
          <div className="rounded-lg border border-border bg-muted/40 p-3 text-xs space-y-2">
            <div className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-1.5 font-semibold text-foreground truncate">
                <UserCheck className="h-3.5 w-3.5 text-primary shrink-0" />
                <span className="truncate">{user?.email ?? 'operator@local.noc'}</span>
              </span>
              <span className="text-[10px] font-bold uppercase px-1.5 py-0.5 rounded bg-primary/15 text-primary shrink-0">
                {user?.role ?? 'admin'}
              </span>
            </div>
            <div className="flex items-center gap-2 pt-0.5">
              <button
                type="button"
                onClick={openLoginModal}
                className="flex-1 rounded border border-border bg-background py-1 px-2 text-[11px] font-medium text-foreground hover:bg-muted transition-colors"
              >
                Switch Operator
              </button>
              <button
                type="button"
                onClick={() => void logout()}
                title="Sign out operator session"
                className="inline-flex items-center justify-center rounded border border-border bg-background p-1 text-muted-foreground hover:text-rose-500 hover:bg-muted transition-colors"
              >
                <LogOut className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          {/* Theme switch & Info */}
          <div className="flex items-center justify-between pt-1">
            <button
              type="button"
              onClick={() => setDarkMode(!darkMode)}
              className="inline-flex items-center gap-2 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors p-1.5 rounded-md hover:bg-muted"
              title="Toggle Dark / Light Mode"
            >
              {darkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
              <span>{darkMode ? 'Light mode' : 'Dark NOC mode'}</span>
            </button>

            <a
              href="/api/v1/health"
              target="_blank"
              rel="noreferrer"
              className="text-muted-foreground hover:text-foreground p-1.5 rounded-md hover:bg-muted text-xs"
              title="API Health"
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
          </div>
        </div>
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Top Header Bar */}
        <header className="h-16 border-b border-border bg-card px-8 flex items-center justify-between flex-shrink-0">
          <div>
            <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
              Control Plane
            </span>
            <h2 className="text-lg font-bold text-foreground">
              {getPageTitle(location.pathname)}
            </h2>
          </div>
          <LiveTicker
            isConnected={isConnected}
            payload={payload}
            latencyHistory={latencyHistory}
            lossHistory={lossHistory}
          />
        </header>

        {/* Live Warning / Traffic Spike Alert Banner */}
        {activeAlert && (
          <div className="bg-rose-600 text-white px-8 py-2.5 text-xs font-semibold flex items-center justify-between shadow-md animate-in slide-in-from-top duration-200">
            <div className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 animate-bounce shrink-0" />
              <span>{activeAlert.message}</span>
            </div>
            <button
              type="button"
              onClick={clearAlert}
              className="p-1 rounded hover:bg-rose-700 transition-colors text-xs font-bold"
            >
              ✕ Dismiss
            </button>
          </div>
        )}

        {/* Scrollable Content */}
        <main id="main" className="flex-1 p-8 overflow-y-auto">
          <div className="max-w-7xl mx-auto space-y-6">
            <Routes>
              <Route path="/" element={<Navigate to="/sites" replace />} />
              <Route path="/sites" element={<SitesPage />} />
              <Route path="/devices" element={<DevicesPage />} />
              <Route path="/clients" element={<ClientsPage />} />
              <Route path="/networks" element={<NetworksPage />} />
              <Route path="/topology" element={<TopologyPage />} />
              <Route path="*" element={<NotFoundPage />} />
            </Routes>
          </div>
        </main>

        {/* Footer */}
        <footer className="border-t border-border bg-card px-8 py-3 text-xs text-muted-foreground flex items-center justify-between flex-shrink-0">
          <span>
            Read-only view of the cache. Data comes from our own API (<code>/api/v1</code>); the browser never talks to the UniFi controller.
          </span>
          <span>v1.0-phase1</span>
        </footer>
      </div>
    </div>
  )
}

function NotFoundPage() {
  return (
    <section aria-labelledby="not-found-heading" className="p-8 text-center">
      <h2 id="not-found-heading" className="text-xl font-bold mb-2">Page not found</h2>
      <p className="text-muted-foreground text-sm">
        Use the sidebar navigation to reach the sites, devices, clients, or networks view.
      </p>
    </section>
  )
}
