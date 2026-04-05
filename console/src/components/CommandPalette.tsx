import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'

interface Command {
  id: string
  label: string
  section: string
  action: () => void
}

export default function CommandPalette() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()

  const commands: Command[] = [
    { id: 'nav-dashboard', label: 'Go to Dashboard', section: 'Navigation', action: () => navigate('/dashboard') },
    { id: 'nav-mcplex', label: 'Go to MCPlex', section: 'Navigation', action: () => navigate('/mcplex') },
    { id: 'nav-a2aplex', label: 'Go to A2APlex', section: 'Navigation', action: () => navigate('/a2aplex') },
    { id: 'nav-llmplex', label: 'Go to LLMPlex', section: 'Navigation', action: () => navigate('/llmplex') },
    { id: 'nav-agents', label: 'Go to Agents', section: 'Navigation', action: () => navigate('/agents') },
    { id: 'nav-permissions', label: 'Go to Permissions', section: 'Navigation', action: () => navigate('/permissions') },
    { id: 'action-deploy', label: 'Deploy new instance', section: 'Actions', action: () => navigate('/deploy') },
    { id: 'action-instances', label: 'List all instances', section: 'Actions', action: () => navigate('/mcplex') },
  ]

  const filtered = query
    ? commands.filter((c) => c.label.toLowerCase().includes(query.toLowerCase()))
    : commands

  const run = useCallback(
    (cmd: Command) => {
      setOpen(false)
      setQuery('')
      cmd.action()
    },
    [],
  )

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        setOpen((prev) => !prev)
        setQuery('')
        setSelected(0)
      }
      if (e.key === 'Escape') {
        setOpen(false)
      }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])

  useEffect(() => {
    if (open) {
      setTimeout(() => inputRef.current?.focus(), 0)
    }
  }, [open])

  useEffect(() => {
    setSelected(0)
  }, [query])

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelected((prev) => Math.min(prev + 1, filtered.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelected((prev) => Math.max(prev - 1, 0))
    } else if (e.key === 'Enter' && filtered[selected]) {
      e.preventDefault()
      run(filtered[selected])
    }
  }

  if (!open) return null

  const sections = Array.from(new Set(filtered.map((c) => c.section)))

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[20vh]">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/50" onClick={() => setOpen(false)} />

      {/* Palette */}
      <div className="relative w-full max-w-lg bg-white dark:bg-gray-900 rounded-xl shadow-2xl overflow-hidden">
        {/* Search input */}
        <div className="flex items-center border-b border-gray-200 dark:border-gray-700 px-4">
          <svg className="w-5 h-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Type a command..."
            className="flex-1 px-3 py-3 text-sm bg-transparent text-gray-900 dark:text-white placeholder-gray-400 focus:outline-none"
          />
          <kbd className="hidden sm:inline-block px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 rounded">
            Esc
          </kbd>
        </div>

        {/* Results */}
        <div className="max-h-72 overflow-y-auto py-2">
          {filtered.length === 0 ? (
            <p className="px-4 py-6 text-sm text-gray-400 text-center">No matching commands.</p>
          ) : (
            sections.map((section) => {
              const items = filtered.filter((c) => c.section === section)
              return (
                <div key={section}>
                  <p className="px-4 pt-2 pb-1 text-xs font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider">
                    {section}
                  </p>
                  {items.map((cmd) => {
                    const idx = filtered.indexOf(cmd)
                    return (
                      <button
                        key={cmd.id}
                        onClick={() => run(cmd)}
                        onMouseEnter={() => setSelected(idx)}
                        className={`w-full text-left px-4 py-2 text-sm transition-colors ${
                          idx === selected
                            ? 'bg-brand-600 text-white'
                            : 'text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-800'
                        }`}
                      >
                        {cmd.label}
                      </button>
                    )
                  })}
                </div>
              )
            })
          )}
        </div>
      </div>
    </div>
  )
}
