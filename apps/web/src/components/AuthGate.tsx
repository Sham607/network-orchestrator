import { createContext, useContext, useState, type FormEvent, type ReactNode } from 'react'
import { Activity, AlertTriangle, Lock, ShieldCheck } from 'lucide-react'
import { ApiError } from '../api/errors'
import { useCurrentUser, useLogin, useLogout } from '../api/queries'
import type { User } from '../api/types'

interface AuthContextValue {
  user: User | null
  canOperate: boolean
  isAdmin: boolean
  openLoginModal: () => void
  logout: () => Promise<void>
}

const defaultOperator: User = {
  id: '00000000-0000-4000-8000-000000000001',
  email: 'operator@local.noc',
  role: 'admin',
}

const AuthContext = createContext<AuthContextValue>({
  user: defaultOperator,
  canOperate: true,
  isAdmin: true,
  openLoginModal: () => {},
  logout: async () => {},
})

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  return useContext(AuthContext)
}

interface LoginScreenProps {
  isModal?: boolean
  onClose?: () => void
}

export function LoginScreen({ isModal = false, onClose }: LoginScreenProps) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const loginMutation = useLogin()

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setErrorMsg(null)
    try {
      await loginMutation.mutateAsync({ email: email.trim(), password })
      if (onClose) onClose()
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        setErrorMsg(err.message)
      } else {
        setErrorMsg(err instanceof Error ? err.message : 'Login failed')
      }
    }
  }

  const card = (
    <div className="w-full max-w-md rounded-xl border border-border bg-card p-8 shadow-2xl space-y-6">
      <div className="flex items-center justify-between pb-4 border-b border-border">
        <div className="flex items-center gap-3.5">
          <div className="h-11 w-11 rounded-lg bg-primary flex items-center justify-center text-primary-foreground shadow-sm">
            <Activity className="h-6 w-6" />
          </div>
          <div>
            <h1 className="text-base font-bold tracking-tight text-foreground">
              Network Orchestrator
            </h1>
            <p className="text-xs font-medium text-muted-foreground flex items-center gap-1.5 mt-0.5">
              <span className="h-2 w-2 rounded-full bg-emerald-500" />
              UniFi Control Plane · Operator Sign In
            </p>
          </div>
        </div>
        {isModal && onClose && (
          <button
            type="button"
            onClick={onClose}
            className="text-xs text-muted-foreground hover:text-foreground px-2 py-1 rounded hover:bg-muted"
          >
            ✕
          </button>
        )}
      </div>

      {errorMsg && (
        <div
          role="alert"
          className="rounded-lg border border-rose-500/30 bg-rose-500/10 p-3 text-xs text-rose-600 dark:text-rose-300 flex items-center gap-2"
        >
          <AlertTriangle className="h-4 w-4 shrink-0" />
          <span>{errorMsg}</span>
        </div>
      )}

      <form onSubmit={(e) => void handleSubmit(e)} className="space-y-4">
        <div>
          <label
            htmlFor="operator-email"
            className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5"
          >
            Operator Email
          </label>
          <input
            id="operator-email"
            type="email"
            required
            autoComplete="email"
            placeholder="budi@kantor.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full rounded-lg border border-border bg-background px-3.5 py-2.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
          />
        </div>

        <div>
          <label
            htmlFor="operator-password"
            className="block text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1.5"
          >
            Password
          </label>
          <input
            id="operator-password"
            type="password"
            required
            minLength={8}
            autoComplete="current-password"
            placeholder="••••••••••••"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-lg border border-border bg-background px-3.5 py-2.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
          />
        </div>

        <button
          type="submit"
          disabled={loginMutation.isPending}
          className="w-full rounded-lg bg-primary py-2.5 px-4 text-sm font-semibold text-primary-foreground shadow hover:opacity-95 disabled:opacity-50 inline-flex items-center justify-center gap-2"
        >
          <Lock className="h-4 w-4" />
          <span>{loginMutation.isPending ? 'Signing in…' : 'Sign In to NOC Dashboard'}</span>
        </button>
      </form>

      <div className="rounded-lg border border-border bg-muted/40 p-3 text-[11px] text-muted-foreground space-y-1">
        <div className="font-semibold text-foreground flex items-center gap-1.5">
          <ShieldCheck className="h-3.5 w-3.5 text-emerald-500" />
          <span>Zero-Trust Operator Session (ADR-002 §6)</span>
        </div>
        <div>• Argon2id password verification · Server-side Postgres session</div>
        <div>• HttpOnly + SameSite=Lax cookie · RBAC: viewer / operator / admin</div>
      </div>
    </div>
  )

  if (isModal) {
    return (
      <div className="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm flex items-center justify-center p-4">
        {card}
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center p-6">
      {card}
    </div>
  )
}

export function AuthProvider({
  children,
  probeOnMount = true,
}: {
  children: ReactNode
  probeOnMount?: boolean
}) {
  const [loginModalOpen, setLoginModalOpen] = useState(false)
  const meQuery = useCurrentUser(probeOnMount)
  const logoutMutation = useLogout()

  const isUnauthorized =
    meQuery.isError &&
    meQuery.error instanceof ApiError &&
    meQuery.error.code === 'unauthorized'

  const activeUser: User | null = isUnauthorized
    ? null
    : (meQuery.data ?? defaultOperator)

  const canOperate = activeUser ? activeUser.role === 'operator' || activeUser.role === 'admin' : false
  const isAdmin = activeUser ? activeUser.role === 'admin' : false

  async function handleLogout() {
    await logoutMutation.mutateAsync()
  }

  if (isUnauthorized) {
    return <LoginScreen />
  }

  return (
    <AuthContext.Provider
      value={{
        user: activeUser,
        canOperate,
        isAdmin,
        openLoginModal: () => setLoginModalOpen(true),
        logout: handleLogout,
      }}
    >
      {children}
      {loginModalOpen && (
        <LoginScreen isModal onClose={() => setLoginModalOpen(false)} />
      )}
    </AuthContext.Provider>
  )
}
