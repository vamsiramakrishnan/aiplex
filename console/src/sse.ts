// Incremental SSE framing for authenticated fetch streams. The decoder handles
// UTF-8 split across network chunks and retains one bounded pending event.
export async function consumeSSE(stream: ReadableStream<Uint8Array>, onEvent: (event: string, data: string) => void): Promise<void> {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  try {
    while (true) {
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
        if (data.length) onEvent(event, data.join('\n'))
      }
      if (buffer.length > 1_048_576) throw new Error('Event too large')
    }
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
