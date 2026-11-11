import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { AuthProvider } from '../components/AuthGate'
import { DeviceDrawer } from '../components/DeviceDrawer'
import {
  deviceListFixture,
  installFetch,
  jsonResponse,
  renderWithProviders,
} from './utils'

describe('AuthGate & RBAC enforcement', () => {
  it('renders LoginScreen when GET /api/v1/me returns 401 unauthorized', async () => {
    installFetch((url) => {
      if (url.startsWith('/api/v1/me')) {
        return jsonResponse(
          { code: 'unauthorized', message: 'authentication required' },
          401,
        )
      }
      return jsonResponse({ status: 'ok' })
    })

    renderWithProviders(
      <AuthProvider>
        <div>Protected Dashboard Content</div>
      </AuthProvider>,
    )

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Sign In to NOC Dashboard/i })).toBeInTheDocument()
    })
    expect(screen.queryByText('Protected Dashboard Content')).not.toBeInTheDocument()
  })

  it('locks AP maintenance buttons for viewer role and shows normalized radios', async () => {
    installFetch((url) => {
      if (url.startsWith('/api/v1/me')) {
        return jsonResponse({
          id: '11111111-2222-4333-8444-555555555555',
          email: 'viewer@kantor.com',
          role: 'viewer',
        })
      }
      return jsonResponse({})
    })

    const apDevice = {
      ...deviceListFixture.data[0],
      device_type: 'ap' as const,
      radios: [
        {
          wlan_standard: '802.11ax',
          frequency_ghz: 5,
          channel_width_mhz: 40,
          channel: 149,
        },
      ],
    }

    renderWithProviders(
      <AuthProvider>
        <DeviceDrawer device={apDevice} onClose={() => {}} />
      </AuthProvider>,
    )

    await waitFor(() => {
      expect(screen.getByText(/Maintenance actions require/i)).toBeInTheDocument()
    })
    expect(screen.getByText(/5GHz · Ch 149 \(40MHz\)/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Reboot Access Point/i })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /Radio Tuning/i }))
    expect(screen.getByText(/Radio tuning requires/i)).toBeInTheDocument()
  })
})
