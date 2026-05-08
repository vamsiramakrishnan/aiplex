import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './hooks/useAuth'
import { ToastProvider } from './components/Toast'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import MCPlex from './pages/MCPlex'
import A2APlex from './pages/A2APlex'
import LLMPlex from './pages/LLMPlex'
import SkillsPlex from './pages/SkillsPlex'
import Agents from './pages/Agents'
import Deploy from './pages/Deploy'
import Permissions from './pages/Permissions'
import Login from './pages/Login'
import InstanceDetail from './pages/InstanceDetail'
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
          <Route path="/mcplex" element={<MCPlex />} />
          <Route path="/a2aplex" element={<A2APlex />} />
          <Route path="/llmplex" element={<LLMPlex />} />
          <Route path="/skillsplex" element={<SkillsPlex />} />
          <Route path="/agents" element={<Agents />} />
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
      <ToastProvider>
        <AppRoutes />
      </ToastProvider>
    </AuthProvider>
  )
}
