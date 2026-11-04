// Package mock holds in-memory implementations of the contracts the oracle depends on, for unit tests.
//
// Each mock records how it was called and fails on demand through its Err fields.
// They are not safe for concurrent use, except Store and Cache, which the API calls from many goroutines.
package mock
