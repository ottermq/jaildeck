import { api } from '../services/api'

// A free name for a new jail or guest, adjective and animal (#148).
export async function suggestName(): Promise<string> {
    return (await api<{ name: string }>('/names/suggest')).name
}
