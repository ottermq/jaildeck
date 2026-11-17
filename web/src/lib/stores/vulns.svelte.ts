import { api } from '../services/api'
import type { VulnSnapshot } from '../vulns'

// getVulnerabilities reads the last package audit in full (#230). It runs
// nothing: the server audits on a timer of its own.
export function getVulnerabilities(): Promise<VulnSnapshot> {
    return api<VulnSnapshot>('/vulnerabilities')
}
