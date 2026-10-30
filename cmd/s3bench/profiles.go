package main

import (
	"fmt"
	"strings"
)

// link is the delay that Toxiproxy adds to one network path: a round trip
// time and a jitter, both in whole milliseconds. Toxiproxy takes whole
// milliseconds only, so a path below 1 ms adds no delay: the run then
// measures the loopback path through the proxy.
type link struct {
	RTTMs int `json:"rttMs"`
	// JitterMs is the bound of the uniform jitter of one direction: each
	// direction adds a value in [-JitterMs, +JitterMs].
	JitterMs int `json:"jitterMs"`
}

// toxicPair is the delay of the request direction and of the answer
// direction of one path.
type toxicPair struct {
	up   latencyAttrs
	down latencyAttrs
}

// split answers the toxic of the request direction and of the answer
// direction: half of the round trip each, the odd millisecond on the answer.
func (l link) split() toxicPair {
	const directions = 2

	half := l.RTTMs / directions

	return toxicPair{
		up:   latencyAttrs{Latency: half, Jitter: l.JitterMs},
		down: latencyAttrs{Latency: l.RTTMs - half, Jitter: l.JitterMs},
	}
}

// profile is one network profile of the run: the delay on the S3 path and on
// the PostgreSQL path, with the measured values it stands for.
type profile struct {
	Name string `json:"name"`
	S3   link   `json:"s3"`
	DB   link   `json:"db"`
	// Measured is the measured round trip and jitter that the profile stands
	// for. docs/reference/s3-latency-benchmark.md lists each source with its
	// date and whether it was fetched.
	Measured string `json:"measured"`
	// Limit marks a step of the limit sweep.
	Limit bool `json:"limit"`
}

// both answers a link used for the S3 path and the PostgreSQL path.
func both(name string, l link, measured string) profile {
	return profile{Name: name, S3: l, DB: l, Measured: measured, Limit: false}
}

// standardProfiles answers the five deployment profiles. Each one puts the
// server at the same distance from S3 and from PostgreSQL, which is the case
// that costs the database-arbitrated design the most. The jitter bound j of
// one direction gives a standard deviation of j*sqrt(2/3) over the round
// trip, so j is the measured standard deviation times sqrt(3/2), rounded.
func standardProfiles() []profile {
	const (
		crossAZRTT           = 1
		metroRTT             = 10
		metroJitter          = 1
		continentRTT         = 65
		continentJitter      = 4
		intercontinentRTT    = 150
		intercontinentJitter = 6
	)

	return []profile{
		both("same-az", link{RTTMs: 0, JitterMs: 0},
			"0.05-0.3 ms RTT, stddev below 0.1 ms; below the 1 ms step of Toxiproxy"),
		both("cross-az", link{RTTMs: crossAZRTT, JitterMs: 0},
			"0.5-1.7 ms RTT, p99-p50 0.1-0.3 ms; jitter below the 1 ms step"),
		both("eu-metro", link{RTTMs: metroRTT, JitterMs: metroJitter},
			"Frankfurt, Amsterdam, Paris: 6-8 ms ISP, 10-13 ms cloud; stddev about 1 ms"),
		both("continent", link{RTTMs: continentRTT, JitterMs: continentJitter},
			"US East-West 56-73 ms, Singapore-Tokyo 68-72 ms; stddev about 3 ms"),
		both("intercontinental", link{RTTMs: intercontinentRTT, JitterMs: intercontinentJitter},
			"Virginia-Tokyo 145-162 ms, Frankfurt-Singapore 153-167 ms; stddev about 5 ms"),
	}
}

// limitSteps answers the steps of the limit sweep: the S3 round trip rises
// while PostgreSQL stays at loopback, then the PostgreSQL round trip rises
// while S3 stays at loopback.
func limitSteps(s3Steps, dbSteps []int) []profile {
	out := make([]profile, 0, len(s3Steps)+len(dbSteps))

	for _, rtt := range s3Steps {
		out = append(out, profile{
			Name:     fmt.Sprintf("limit-s3-%dms", rtt),
			S3:       link{RTTMs: rtt, JitterMs: 0},
			DB:       link{RTTMs: 0, JitterMs: 0},
			Measured: "limit sweep",
			Limit:    true,
		})
	}

	for _, rtt := range dbSteps {
		out = append(out, profile{
			Name:     fmt.Sprintf("limit-db-%dms", rtt),
			S3:       link{RTTMs: 0, JitterMs: 0},
			DB:       link{RTTMs: rtt, JitterMs: 0},
			Measured: "limit sweep",
			Limit:    true,
		})
	}

	return out
}

// selectProfiles answers the profiles of all whose names are in the
// comma-separated list names, or all of them for "all".
func selectProfiles(all []profile, names string) ([]profile, error) {
	if names == "all" {
		return all, nil
	}

	var out []profile

	for name := range strings.SplitSeq(names, ",") {
		found := false

		for _, p := range all {
			if p.Name == strings.TrimSpace(name) {
				out = append(out, p)
				found = true
			}
		}

		if !found {
			return nil, fmt.Errorf("%w: unknown profile %q", errBadFlag, name)
		}
	}

	return out, nil
}
