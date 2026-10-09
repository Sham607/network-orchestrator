import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// Vitest runs without globals (every test imports what it uses), so
// testing-library's automatic cleanup is not registered for us.
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})
