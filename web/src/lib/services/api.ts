import { onUnauthenticated } from '../stores/auth.svelte';


type Options = RequestInit & { skipAuthRedirect?: boolean }

export class ApiError extends Error {
    public readonly statusCode: number;
    public readonly statusText?: string;
    // The decoded body, when the failure had one. A refusal is not always a
    // sentence: a 409 from the snapshot and dataset endpoints carries the
    // structure that explains it -- which snapshots are newer, which clones
    // stand in the way, what mountpoint the dataset already has. Dropping it
    // would leave the interface saying "conflict" where the API said why.
    public readonly body?: unknown;

    constructor(statusCode: number, statusText: string, body?: unknown){
        super(statusText)

        this.statusCode = statusCode
        this.statusText = statusText;
        this.body = body;

        this.name = 'ApiError';

        Object.setPrototypeOf(this, ApiError.prototype)

    }
}

export async function api<T>(
    path: string, 
    opts: Options = {}
): Promise<T> {
  
    const res = await fetch(`/api${path}`, {
        ...opts,
        headers: { 'Content-Type': 'application/json', ...opts.headers },
    });

    if (res.status === 401 && !opts.skipAuthRedirect) {
        onUnauthenticated()               // clears auth state; the app renders login
        throw new ApiError(401, 'session expired')
    }
  
    if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        
        throw new ApiError(
            res.status, 
            body.error ?? res.statusText,
            body
        );
    }

    if (res.status === 204) 
        return undefined as T   // logout sends no body
    
    return res.json()
}