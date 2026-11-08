import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './hooks/useAuth'
import { ToastProvider } from './components/Toast'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import { consoleModules } from './modules'
import { ModuleProvider, RequireModule } from './hooks/useModules'
import Agents from './pages/Agents'
import Deploy from './pages/Deploy'
import Permissions from './pages/Permissions'
import Login from './pages/Login'
import InstanceDetail from './pages/InstanceDetail'
import Runs from './pages/Runs'
import CommandPalette from './components/CommandPalette'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuth()
  const location = useLocation()

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
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
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<Dashboard />} />
          {consoleModules.map(({ name, path, component: Component }) => (
            <Route key={name} path={path} element={<RequireModule name={name}><Component /></RequireModule>} />
          ))}
          <Route path="/agents" element={<Agents />} />
          <Route path="/runs" element={<Runs />} />
          <Route path="/deploy" element={<Deploy />} />
          <Route path="/permissions" element={<Permissions />} />
          <Route path="/instances/:id" element={<InstanceDetail />} />
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
