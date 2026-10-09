// Package inventory owns the local cache of UniFi state (sites, devices,
// clients, networks), the sync worker and the sync_runs history. It depends on
// unifi and audit; the api package depends on it, never the reverse.
package inventory
