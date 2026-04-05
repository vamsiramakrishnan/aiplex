import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'

export default function Login() {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const { login } = useAuth()
  const navigate = useNavigate()

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    const raw = token.trim()
    if (!raw) {
      setError('Please enter a token.')
      return
    }

    const jwt = raw.startsWith('Bearer ') ? raw.slice(7) : raw
    const parts = jwt.split('.')
    if (parts.length !== 3) {
      setError('Invalid token format. Expected a JWT (three dot-separated segments).')
      return
    }

    try {
      const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
      if (!payload.sub) {
        setError('Token is missing a "sub" claim.')
        return
      }
    } catch {
      setError('Could not decode token payload.')
      return
    }

    login(raw)
    navigate('/dashboard', { replace: true })
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-950 px-4">
      <div className="w-full max-w-md bg-white dark:bg-gray-900 rounded-xl shadow-lg p-8">
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">AIPlex</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
            Unified AI Control Plane
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label htmlFor="token" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Bearer Token
            </label>
            <textarea
              id="token"
              rows={4}
              value={token}
              onChange={(e) => {
                setToken(e.target.value)
                setError('')
              }}
              placeholder="Paste your JWT token here..."
              className="w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-800 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent font-mono"
            />
          </div>

          {error && (
            <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
          )}

          <button
            type="submit"
            className="w-full py-2.5 bg-brand-600 hover:bg-brand-700 text-white text-sm font-medium rounded-lg transition-colors"
          >
            Sign In
          </button>
        </form>

        <p className="mt-6 text-xs text-gray-400 dark:text-gray-500 text-center">
          Obtain a token via OAuth 2.1 device grant or authorization code flow.
        </p>
      </div>
    </div>
  )
}
