import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import MCPlex from './pages/MCPlex'
import A2APlex from './pages/A2APlex'
import LLMPlex from './pages/LLMPlex'
import Agents from './pages/Agents'
import Deploy from './pages/Deploy'

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/mcplex" element={<MCPlex />} />
        <Route path="/a2aplex" element={<A2APlex />} />
        <Route path="/llmplex" element={<LLMPlex />} />
        <Route path="/agents" element={<Agents />} />
        <Route path="/deploy" element={<Deploy />} />
      </Route>
    </Routes>
  )
}
