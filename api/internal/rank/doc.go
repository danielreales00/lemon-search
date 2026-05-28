// Package rank scores a slice of candidate businesses using the 7 signals
// described in the spec, weighted per archetype from config.
//
// All scoring is pure and synchronous so it can be unit-tested against
// fixture candidates without spinning up Postgres.
package rank
