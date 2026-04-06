import { useEffect, useState, useRef } from 'react';

interface SSEStats {
  total_instances: number;
  running: number;
  mcplex: number;
  a2aplex: number;
  llmplex: number;
  agents: number;
  delegations: number;
  denials: number;
  timestamp: string;
}

export function useSSE(enabled: boolean = true): SSEStats | null {
  const [stats, setStats] = useState<SSEStats | null>(null);
  const eventSourceRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (!enabled) return;

    const token = localStorage.getItem('aiplex_token');
    // EventSource doesn't support custom headers, so use query param for auth
    const url = `/api/v1/events/stream${token ? `?token=${token}` : ''}`;

    const es = new EventSource(url);
    eventSourceRef.current = es;

    es.addEventListener('stats', (event) => {
      try {
        const data = JSON.parse(event.data) as SSEStats;
        setStats(data);
      } catch {
        // ignore parse errors
      }
    });

    es.onerror = () => {
      // EventSource auto-reconnects, just log
      console.warn('SSE connection error, reconnecting...');
    };

    return () => {
      es.close();
      eventSourceRef.current = null;
    };
  }, [enabled]);

  return stats;
}
