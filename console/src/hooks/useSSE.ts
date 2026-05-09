import { useEffect, useState, useRef } from 'react';

export interface SSEStats {
  total_instances: number;
  running: number;
  instances_by_kind: Record<string, number>;
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
    const url = `/events/stream${token ? `?token=${token}` : ''}`;

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
      console.warn('SSE connection error, reconnecting...');
    };

    return () => {
      es.close();
      eventSourceRef.current = null;
    };
  }, [enabled]);

  return stats;
}
