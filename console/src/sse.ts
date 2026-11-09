// Incremental SSE framing for authenticated fetch streams. The decoder handles
// UTF-8 split across network chunks and retains one bounded pending event.
export async function consumeSSE(stream: ReadableStream<Uint8Array>, onEvent: (event: string, data: string) => void, signal?: AbortSignal): Promise<void> {
  const reader = stream.getReader()
  const abort = () => { void reader.cancel().catch(() => {}) }
  signal?.addEventListener('abort', abort, { once: true })
  const decoder = new TextDecoder()
  let buffer = ''
  try {
    while (true) {
      if (signal?.aborted) break
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let boundary: RegExpExecArray | null
      while ((boundary = /\r?\n\r?\n/.exec(buffer)) !== null) {
        if (boundary.index > 1_048_576) throw new Error('Event too large')
        const frame = buffer.slice(0, boundary.index)
        buffer = buffer.slice(boundary.index + boundary[0].length)
        let event = 'message'
        const data: string[] = []
        for (const line of frame.split(/\r?\n/)) {
          if (line.startsWith('event:')) event = line.slice(6).replace(/^ /, '')
          if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''))
        }
        if (data.length && !signal?.aborted) onEvent(event, data.join('\n'))
      }
      if (buffer.length > 1_048_576) throw new Error('Event too large')
    }
  } finally {
    signal?.removeEventListener('abort', abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

// One connection; callers own reconnect and polling policies. Credentials never
// enter URLs, and a late response from a canceled session cannot emit events.
export async function authenticatedSSE(path: string, token: string, signal: AbortSignal, onEvent: (event: string, data: string) => void): Promise<'closed' | 'denied'> {
  if (!path.startsWith('/') || path.startsWith('//') || !token) throw new Error('An authenticated local stream is required')
  const response = await fetch(path, { headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' }, signal, redirect: 'error' })
  if (signal.aborted) { await response.body?.cancel().catch(() => {}); return 'closed' }
  if (response.status === 401 || response.status === 403) {
    if (response.status === 401) window.dispatchEvent(new CustomEvent('aiplex:session-invalid', { detail: { token } }))
    await response.body?.cancel().catch(() => {})
    return 'denied'
  }
  if (!response.ok || !response.body) throw new Error('Event stream unavailable')
  if (response.headers?.get('Content-Type') && !response.headers.get('Content-Type')!.toLowerCase().startsWith('text/event-stream')) { await response.body.cancel(); throw new Error('Invalid event stream content type') }
  await consumeSSE(response.body, onEvent, signal)
  return 'closed'
}
