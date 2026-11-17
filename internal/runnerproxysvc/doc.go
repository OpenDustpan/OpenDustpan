// Package runnerproxysvc is the runtime guard of the job proxy: it decides
// where the proxy runs for a claim, starts one proxy per claim, keeps the
// sandbox route on the proxy across a restart, and writes the blocked line
// of a refusal to the job log.
//
// A unit with no proxy rules gets no proxy and its routing stays unchanged.
// A claim with proxy rules and process isolation fails: process isolation
// has no kernel boundary.
package runnerproxysvc
