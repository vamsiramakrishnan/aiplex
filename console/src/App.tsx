import { Routes, Route, Navigate, Link, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './hooks/useAuth'
import { ToastProvider } from './components/Toast'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import { consoleModules } from './modules'
import { ModuleProvider, RequireModule } from './hooks/useModules'
import Agents from './pages/Agents'
import Overview from './pages/Overview'
import AccessPoints from './pages/AccessPoints'
import UserIdentity from './pages/UserIdentity'
import Deploy from './pages/Deploy'
import Permissions from './pages/Permissions'
import Login from './pages/Login'
import InstanceDetail from './pages/InstanceDetail'
import Runs from './pages/Runs'
import { QuotaUsage } from './pages/QuotaTabs'
import Usage from './pages/Usage'
import { RequireRole } from './components/Forbidden'
import CommandPalette from './components/CommandPalette'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, verifying } = useAuth()
  const location = useLocation()

  if (verifying) return <p role="status" className="p-6 text-sm">Verifying identity…</p>
  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}

function NotFound() {
  const { pathname } = useLocation()
  return (
    <section role="alert" className="max-w-xl space-y-2">
      <h1 className="text-xl font-semibold">Page not found</h1>
      <p className="text-sm text-gray-600 dark:text-gray-400">There is no screen at <code>{pathname}</code>.</p>
      <p><Link to="/overview" className="text-sm text-brand-600 hover:underline">Go to Overview</Link></p>
    </section>
  )
}

function AppRoutes() {
  return (
    <>
      <CommandPalette />
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          element={
            <RequireAuth>
              <Layout />
            </RequireAuth>
          }
        >
          <Route path="/" element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<Overview />} />
          <Route path="/dashboard" element={<RequireRole need="admin" what="The dashboard"><Dashboard /></RequireRole>} />
          {consoleModules.map(({ name, path, component: Component }) => (
            <Route key={name} path={path} element={<RequireRole need="operator" what="This screen"><RequireModule name={name}><Component /></RequireModule></RequireRole>} />
          ))}
          <Route path="/quotaplex/usage" element={<RequireRole need="operator" what="QuotaPlex"><RequireModule name="quotaplex"><QuotaUsage /></RequireModule></RequireRole>} />
          <Route path="/usage" element={<Usage />} />
          <Route path="/agents" element={<RequireRole need="operator" what="Agents"><Agents /></RequireRole>} />
          <Route path="/access-points/*" element={<AccessPoints />} />
          <Route path="/identity" element={<Navigate to="/identity/users" replace />} />
          <Route path="/identity/users" element={<RequireRole need="operator" what="User identity"><UserIdentity /></RequireRole>} />
          <Route path="/runs" element={<RequireRole need="operator" what="Runs"><Runs /></RequireRole>} />
          <Route path="/deploy" element={<RequireRole need="operator" what="Deploy"><Deploy /></RequireRole>} />
          <Route path="/permissions" element={<RequireRole need="admin" what="Permissions"><Permissions /></RequireRole>} />
          <Route path="/instances/:id" element={<RequireRole need="operator" what="Instances"><InstanceDetail /></RequireRole>} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <ModuleProvider>
        <ToastProvider>
          <AppRoutes />
        </ToastProvider>
      </ModuleProvider>
    </AuthProvider>
  )
}
