import { api } from "../services/api";

export interface Principal {
    kind: 'session' | 'token'
    name: string
    expires_at?:string // absent for tokens; Go sends omitempty
    // What it may do beyond the plain API (#178): "console" unless the host
    // has turned the browser console off (JAILDECK_CONSOLE_UI=off, #155).
    // Absent from a server older than scopes, which had no such switch.
    scopes?: string[]
}    

export const auth = $state<{ principal: Principal | null; checked: boolean}>({
    principal: null,
    checked: false,
});


export function onUnauthenticated():void {
    
    auth.principal = null;
    auth.checked = true;
}

export async function loadPrincipal(): Promise<void> {
    try{
        const res = await api<{principal:Principal}>('/auth/me', {skipAuthRedirect: true})
        auth.principal = res.principal
    }
    catch {
        // both 401 and a dead backend will fall in this case, 
        // so Login page will show 'Could not reach Jail Deck' for second case
        auth.principal = null
    }
    auth.checked = true;
}

export async function login(password:string): Promise<void> {
    await api<{authenticated:boolean}>('/auth/login', 
        {
            method:'POST',
            body: JSON.stringify({password}),
            skipAuthRedirect:true
        }
    )
    await loadPrincipal()
}

export async function logout(): Promise<void> {
    try{
        await api<void>('/auth/logout',{method:'POST'})
    } catch  {}
    onUnauthenticated()
}

// Whether this login may open a console. A server from before scopes said
// nothing, and every login could.
export function mayOpenConsole(): boolean {
    return auth.principal?.scopes?.includes('console') ?? true
}
