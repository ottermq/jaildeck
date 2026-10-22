import { api } from "../services/api";
import type { Operation } from "./jails.svelte";

export interface AuditEntry {
    timestamp: string
    operation: string
    target: string
    command: string
    exit_code: number
    success: boolean
    timed_out?: boolean
    error?: string
}

export interface AuditQuery {
    operation?: Operation
    success?: boolean
    timedOut?: boolean
    limit?: number
    targets?: string[]
    // Entries strictly older than this RFC 3339 timestamp. The cursor for
    // paging backwards: pass the timestamp of the oldest row already shown.
    before?: string
}

export type Outcome = 'succeeded' | 'timed out' | 'failed'

export function getAuditOutcome(e: AuditEntry): Outcome {
    if (e.success) return 'succeeded'
    return e.timed_out?'timed out':'failed'
}

export function outcomeToQuery(o:Outcome|undefined): Partial<AuditQuery> {
    switch(o) {
        case 'succeeded':   return { success:true }
        case 'failed':      return { success:false, timedOut:false}
        case 'timed out':   return { timedOut:true }
        default:            return {}
    }
}

export async function listOperations(q: AuditQuery|null = null): Promise<AuditEntry[]> {
    const p =  new URLSearchParams()
    if (q?.operation) p.set('operation', q.operation)
    if (q?.success !== undefined) p.set('success', String(q.success))
    if (q?.timedOut!==undefined) p.set('timed_out', String(q.timedOut))      
    if (q?.limit) p.set('limit', String(q.limit))
    if (q?.targets && q.targets.length > 0) p.set('targets', q.targets.join(','))
    if (q?.before) p.set('before', q.before)
    const qs =  p.toString()
    return await api<AuditEntry[]>(`/audit${qs ? `?${qs}`:''}`)
}

