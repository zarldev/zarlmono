// Shared admin client + small utilities used across tab components.
// Each tab imports `client` from here instead of instantiating its own.
import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { AdminService } from '@/gen/zarl/v1/admin_pb'

const transport = createConnectTransport({ baseUrl: window.location.origin })

export const client = createClient(AdminService, transport)

// prettyJSON round-trips a JSON string through parse/stringify with 2-space
// indent; silently returns the original on parse failure so the caller never
// has to branch on validity.
export function prettyJSON(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}
