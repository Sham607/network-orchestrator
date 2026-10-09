import { NavLink, Navigate, Route, Routes } from 'react-router-dom'

import DevicesPage from './pages/DevicesPage'
import SitesPage from './pages/SitesPage'

/**
 * Application shell and routes.
 *
 * Only the two read-only views of the first vertical slice are routed. There is
 * deliberately no write UI: the write endpoints are gated off in phase 1
 * (`403 write_disabled`).
 */
export default function App() {
  return (
    <>
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <header className="app-header">
        <p className="app-title">Network Orchestrator</p>
        <nav aria-label="Primary">
          <NavLink to="/sites">Sites</NavLink>
          <NavLink to="/devices">Devices</NavLink>
        </nav>
      </header>

      <main id="main">
        <Routes>
          <Route path="/" element={<Navigate to="/sites" replace />} />
          <Route path="/sites" element={<SitesPage />} />
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </main>

      <footer className="app-footer">
        Read-only view of the cache. Data comes from our own API (<code>/api/v1</code>); the browser
        never talks to the UniFi controller.
      </footer>
    </>
  )
}

function NotFoundPage() {
  return (
    <section aria-labelledby="not-found-heading">
      <h2 id="not-found-heading">Page not found</h2>
      <p>
        Use the navigation above to reach the sites or devices view.
      </p>
    </section>
  )
}
