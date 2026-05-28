// Package postgres is the Supabase Postgres adapter implementing
// domain.BusinessRepo.
//
// It hides SQL details and connection pooling behind the port interface and
// can be substituted (e.g., for Meilisearch) without touching ranking.
package postgres
