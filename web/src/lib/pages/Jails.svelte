<script lang="ts">
    import { onMount } from "svelte";
    import { listJails, runJailOperation } from "../stores/jails.svelte";
    import type {JailData, Operation} from "../stores/jails.svelte";
    import { ApiError } from "../services/api";

    let jails = $state<JailData[]|null>(null)
    let loadError = $state('')
    
    async function load(){
        loadError = ''
        try {
            jails=await listJails();
        } catch (err) {
            loadError = err instanceof ApiError
                ? err.statusText ?? 'Could not load jails'
                : 'Could not reach Jail Deck'
        }
    }
    
    onMount(()=>{
        load();
    })
    
    let pending = $state<Record<string, Operation>>({})
    let rowError = $state<Record<string, string>>({})
    
    async function run(j:JailData, op:Operation) {
        if (pending[j.name]) return;

        if (op !== 'start' && !confirm(`${capitalizeFirstLetter(op)} jail "${j.name}"? Anything running inside it will be interrupted.`)) return
        pending[j.name] = op
        delete rowError[j.name]
        try{
            await runJailOperation(j.name, op)
        } catch(err) {
            rowError[j.name] = describe(err,op,j.name)
        } finally {
            delete pending[j.name]
            await load()
        }
    }

    function describe(err:unknown, op:Operation, name: string): string {
        if (!(err instanceof ApiError)) return 'Could not reach Jail Deck'
        if (err.statusCode === 504) {
            return `${capitalizeFirstLetter(op)} of ${name} timed out. It may still be running - the list has been refreshed.`
        }
        return err.statusText ?? `Could not ${op} ${name}.`
    }

    function capitalizeFirstLetter(str:string){
        if (!str) return "";
        return str.charAt(0).toUpperCase() + str.slice(1);
    }

    function normalizeEmptyValue(str:string){
        if (!str) return "—";
        return str;
    }


</script>

<div>
    <h1>Jails</h1>
    {#if loadError && jails === null}
        <p class="error">{loadError}</p>
        <button onclick={load}>Retry</button>
    {:else}
        {#if loadError}
        <p class="error">{loadError} <button onclick={load}>Retry</button></p>
        {/if}
        {#if jails === null}
            <p>Loading jails</p>
        {:else if jails.length===0}
            <p>No jails are configured on this host.</p>
        {:else}
        <table>
            <thead>
            <tr>
                <th>JID</th>
                <th>Name</th>
                <th>Status</th>
                <th>Hostname</th>
                <th>IP</th>
                <th>Path</th>
                <th>Action</th>
            </tr>
            </thead>
            <tbody>
                {#each jails as j (j.name) }
                <tr>
                    <td>{normalizeEmptyValue(j.jid)}</td>
                    <td>{j.name}</td>
                    <td>{j.status}</td>
                    <td>{normalizeEmptyValue(j.hostname)}</td>
                    <td>{normalizeEmptyValue(j.ip)}</td>
                    <td>{normalizeEmptyValue(j.path)}</td>
                    <td>
                        <div class="actions">
                            {#if j.status === 'running'}
                                <button 
                                    type="button" 
                                    disabled={!!pending[j.name]}
                                    aria-label={`Stop ${j.name}`}
                                    aria-busy={!!pending[j.name]}
                                    onclick={()=>run(j,'stop')} 
                                    >
                                    {pending[j.name]==='stop'? 'Stopping...':'■ Stop'}
                                </button>
                                <button
                                    type="button"
                                    disabled={!!pending[j.name]}
                                    aria-label={`Restart ${j.name}`}
                                    aria-busy={!!pending[j.name]}
                                    onclick={()=>run(j, 'restart')}
                                >
                                    {pending[j.name] === 'restart' ? 'Restarting...': '↻ Restart'}
                                </button>
                                {:else}
                                <button 
                                    type="button"
                                    disabled={!!pending[j.name]}
                                    aria-label={`Start ${j.name}`}
                                    aria-busy={!!pending[j.name]}
                                    onclick={()=>run(j,'start')} 
                                >
                                    {pending[j.name]==='start'? 'Starting...':'▶ Start'}
                                </button>
                            {/if}                    
                        </div>
                    </td>
                </tr>
                {#if rowError[j.name]}
                <tr class="row-error">
                    <td colspan="7" aria-live="polite">{rowError[j.name]}</td>
                </tr>
                {/if}
                {/each}
            </tbody>
        </table>
        {/if}
    {/if}
</div>

<style>
button {
    padding: 0.4rem 0.8rem;
    border: 1px solid #444;
    border-radius: 4px;
    background: transparent;
    color: inherit;
    cursor: pointer;
}

button:disabled{
    opacity: 0.5;
    cursor: not-allowed;
}

button:hover {
    background: #ffffff15;
}

button:hover:not(:disabled){
    background: #ffffff15;
}

.actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
}



</style>