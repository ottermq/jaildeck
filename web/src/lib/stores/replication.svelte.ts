import { api } from '../services/api'
import type { ReplicationSnapshot } from '../replication'

// getReplication reads the last read of zrepl's cursors (#247). It runs
// nothing: the server reads them every minute.
export function getReplication(): Promise<ReplicationSnapshot> {
    return api<ReplicationSnapshot>('/replication')
}
