import { api } from "../services/api";

export interface JailData {
    jid: string
    name: string
    status: "running" | "stopped"
    hostname: string
    ip: string
    path: string
}

export type Operation = 'start'|'stop'|'restart'


export async function listJails(): Promise<JailData[]>{    
        return await api<JailData[]>('/jails');
}

export async function runJailOperation(name:string, op:Operation): Promise<JailData>{
    return await api<JailData>(`/jails/${encodeURIComponent(name)}/${op}`, {method: 'POST'})
}