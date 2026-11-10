import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import logoLight from '../brand/aiplex-lockup-horizontal.svg'
import logoReversed from '../brand/aiplex-lockup-horizontal-reversed.svg'

export default function Login() {
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const { login, verifying, error: sessionError } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    const raw = token.trim()
    if (!raw) {
      setError('Please enter a token.')
      return
    }

    try {
      await login(raw)
      const from = location.state?.from?.pathname as string | undefined
      navigate(from?.startsWith('/') && !from.startsWith('//') ? from : '/dashboard', { replace: true })
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Sign in failed.')
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-950 px-4">
      <div className="w-full max-w-md bg-white dark:bg-gray-900 rounded-xl shadow-lg p-8">
        <div className="mb-6 text-center">
          <h1 className="flex justify-center">
            <img src={logoLight} alt="AIPlex" className="h-10 w-auto dark:hidden" />
            <img src={logoReversed} alt="AIPlex" className="h-10 w-auto hidden dark:block" />
          </h1>
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

          {(error || sessionError) && (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">{error || sessionError}</p>
          )}

          <button
            type="submit"
            disabled={verifying}
            className="w-full py-2.5 bg-brand-600 hover:bg-brand-700 text-white text-sm font-medium rounded-lg transition-colors"
          >
            {verifying ? 'Verifying identity…' : 'Sign In'}
          </button>
        </form>

        <p className="mt-6 text-xs text-gray-400 dark:text-gray-500 text-center">
          Obtain a token via OAuth 2.1 device grant or authorization code flow.
        </p>
      </div>
    </div>
  )
}
