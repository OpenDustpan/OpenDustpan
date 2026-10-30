package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// results is the full output of the run.
type results struct {
	RunID       string                      `json:"runID"`
	Time        string                      `json:"time"`
	Host        hostInfo                    `json:"host"`
	Plan        plan                        `json:"plan"`
	Conditional map[string]conditionalCheck `json:"conditional"`
	Profiles    []profileResult             `json:"profiles"`
	Limits      []limitResult               `json:"limits"`
}

// hostInfo describes the machine of the run.
type hostInfo struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	CPUs   int    `json:"cpus"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// profileResult holds one profile on every store.
type profileResult struct {
	Profile profile       `json:"profile"`
	Stores  []storeResult `json:"stores"`
}

// storeResult holds every part of one profile on one store.
type storeResult struct {
	Store     string           `json:"store"`
	DBPing    stats            `json:"dbPing"`
	Puts      []putResult      `json:"puts"`
	Sequences []sequenceResult `json:"sequences"`
	Sweep     *sweepResult     `json:"sweep,omitempty"`
	Load      *loadResult      `json:"load,omitempty"`
	Errors    []string         `json:"errors,omitempty"`
}

// addError records err of part, when err is not nil.
func (s *storeResult) addError(part string, err error) {
	if err != nil {
		s.Errors = append(s.Errors, part+": "+err.Error())
	}
}

// limitResult holds one step of the limit sweep.
type limitResult struct {
	Profile profile     `json:"profile"`
	Store   string      `json:"store"`
	DBPing  stats       `json:"dbPing"`
	Load    *loadResult `json:"load,omitempty"`
}

// newResults answers empty results of the run runID for p on this host.
func newResults(p plan, runID string) *results {
	return &results{
		RunID:       runID,
		Time:        time.Now().UTC().Format(time.RFC3339),
		Host:        readHost(),
		Plan:        p,
		Conditional: map[string]conditionalCheck{},
		Profiles:    nil,
		Limits:      nil,
	}
}

// readHost reads the CPU model and the memory size on Linux, and leaves
// them empty elsewhere.
func readHost() hostInfo {
	h := hostInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), CPU: "", Memory: ""}

	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		h.CPU = firstField(string(data), "model name")
	}

	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		h.Memory = firstField(string(data), "MemTotal")
	}

	return h
}

// firstField answers the value of the first "name: value" line of text, or
// "" when no line has the name.
func firstField(text, name string) string {
	for line := range strings.Lines(text) {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

// sizeLabel answers a byte count as KiB or MiB.
func sizeLabel(n int) string {
	if n >= size1MiB {
		return fmt.Sprintf("%d MiB", n/size1MiB)
	}

	const kib = 1 << 10

	return fmt.Sprintf("%d KiB", n/kib)
}

// pair answers "p50 / p99" of s, or the error count when nothing succeeded.
func pair(s stats) string {
	if s.Samples == 0 {
		return fmt.Sprintf("failed (%d errors)", s.Errors)
	}

	out := fmt.Sprintf("%.1f / %.1f", s.P50Ms, s.P99Ms)
	if s.Errors > 0 {
		out += fmt.Sprintf(" (%d errors)", s.Errors)
	}

	return out
}

// flowPair answers "p50 / p99" of a load harness flow.
func flowPair(s flowStats) string {
	return pair(
		stats{Samples: s.Samples, P50Ms: s.P50Ms, P99Ms: s.P99Ms, MaxMs: s.MaxMs, Errors: s.Errors, FirstError: ""},
	)
}

// operation answers the time of a backup or a restore, or its error.
func operation(o operationResult) string {
	switch {
	case !o.Ran:
		return "not run"
	case o.Error != "":
		return fmt.Sprintf("failed after %.0f ms", o.Ms)
	default:
		return fmt.Sprintf("%.0f", o.Ms)
	}
}

// renderMarkdown answers the tables of the run as Markdown.
func renderMarkdown(r *results) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# S3 latency benchmark results\n\nMeasured on %s, %s, %d CPUs (%s), %s memory, %s/%s.\n\n",
		r.Time, r.Host.CPU, r.Host.CPUs, r.Host.Arch, r.Host.Memory, r.Host.OS, r.Host.Arch)

	writeConditional(&b, r)
	writeProfiles(&b, r)
	writePuts(&b, r)
	writeSequences(&b, r)
	writeLoad(&b, r)
	writeSweeps(&b, r)
	writeLimits(&b, r)

	return b.String()
}

// writeConditional writes the If-None-Match check of each store.
func writeConditional(b *strings.Builder, r *results) {
	b.WriteString(
		"## If-None-Match on a second write\n\n| Store | First PUT | Second PUT | Enforced |\n|---|---|---|---|\n",
	)

	for name, c := range r.Conditional {
		fmt.Fprintf(b, "| %s | %d | %d | %t |\n", name, c.FirstStatus, c.SecondStatus, c.Enforced)
	}

	b.WriteString("\n")
}

// writeProfiles writes the profile table with the measured database ping.
func writeProfiles(b *strings.Builder, r *results) {
	b.WriteString("## Profiles\n\nRTT and jitter bound are what Toxiproxy adds on each path. " +
		"The database ping is `SELECT 1` through the proxy (p50 / p99 ms).\n\n" +
		"| Profile | S3 RTT, jitter (ms) | DB RTT, jitter (ms) | DB ping | Stands for |\n|---|---|---|---|---|\n")

	for i := range r.Profiles {
		p := &r.Profiles[i]
		ping := ""
		if len(p.Stores) > 0 {
			ping = pair(p.Stores[0].DBPing)
		}

		fmt.Fprintf(
			b,
			"| %s | %d, ±%d | %d, ±%d | %s | %s |\n",
			p.Profile.Name,
			p.Profile.S3.RTTMs,
			p.Profile.S3.JitterMs,
			p.Profile.DB.RTTMs,
			p.Profile.DB.JitterMs,
			ping,
			p.Profile.Measured,
		)
	}

	b.WriteString("\n")
}

// writePuts writes the blob PUT table.
func writePuts(b *strings.Builder, r *results) {
	b.WriteString("## Blob PUT (p50 / p99 ms)\n\n" +
		"| Profile | Store | Size | Samples | Adapter, If-None-Match (code) | Raw, If-None-Match | " +
		"Raw, plain, unique key (model) |\n|---|---|---|---|---|---|---|\n")

	for i := range r.Profiles {
		p := &r.Profiles[i]

		for j := range p.Stores {
			s := &p.Stores[j]

			bySize := map[int]map[string]stats{}

			var order []int

			for _, put := range s.Puts {
				if bySize[put.SizeBytes] == nil {
					bySize[put.SizeBytes] = map[string]stats{}
					order = append(order, put.SizeBytes)
				}

				bySize[put.SizeBytes][put.Mode] = put.Stats
			}

			for _, size := range order {
				m := bySize[size]
				fmt.Fprintf(b, "| %s | %s | %s | %d | %s | %s | %s |\n", p.Profile.Name, s.Store, sizeLabel(size),
					m[modeAdapter].Samples, pair(m[modeAdapter]), pair(m[modeRawConditional]), pair(m[modeRawPlain]))
			}
		}
	}

	b.WriteString("\n")
}

// writeSequences writes the modeled sequences of both designs.
func writeSequences(b *strings.Builder, r *results) {
	b.WriteString("## Modeled write sequences (p50 / p99 ms)\n\n" +
		"Both columns are models: real requests in the order of each design, not server code.\n\n" +
		"| Profile | Store | Flow | Conditional | Database-arbitrated | Δ p50 |\n|---|---|---|---|---|---|\n")

	for i := range r.Profiles {
		p := &r.Profiles[i]

		for j := range p.Stores {
			s := &p.Stores[j]

			for k := range s.Sequences {
				q := &s.Sequences[k]
				fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %+.1f |\n", p.Profile.Name, s.Store, q.Flow,
					pair(q.Conditional), pair(q.Arbitrated), q.Arbitrated.P50Ms-q.Conditional.P50Ms)
			}
		}
	}

	b.WriteString("\n")
}

// writeLoad writes the CLI flows of the load harness.
func writeLoad(b *strings.Builder, r *results) {
	b.WriteString("## CLI flows through the load harness (p50 / p99 ms)\n\n" +
		"| Profile | Store | OpenTofu state upload | Lock, upload, unlock | Pulumi checkpoint save | " +
		"Update cycle | Backup (ms) | Restore (ms) |\n|---|---|---|---|---|---|---|---|\n")

	for i := range r.Profiles {
		p := &r.Profiles[i]

		for j := range p.Stores {
			s := &p.Stores[j]

			if s.Load == nil {
				continue
			}

			writeLoadRow(b, p.Profile.Name, s.Store, s.Load)
		}
	}

	b.WriteString("\n")
}

// writeLoadRow writes one load harness row.
func writeLoadRow(b *strings.Builder, profileName, store string, l *loadResult) {
	if l.Report == nil {
		fmt.Fprintf(b, "| %s | %s | run failed: %s | | | | | |\n", profileName, store, oneLine(l.Error))

		return
	}

	rep := l.Report
	fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", profileName, store,
		flowPair(rep.StateUpload), flowPair(rep.StateCycle), flowPair(rep.CheckpointSave), flowPair(rep.UpdateCycle),
		operation(rep.Backup), operation(rep.Restore))
}

// writeSweeps writes the orphan sweeps.
func writeSweeps(b *strings.Builder, r *results) {
	b.WriteString("## Orphan sweep (ms)\n\n| Profile | Store | Objects | LIST | Keep set read | DELETEs | Total |\n" +
		"|---|---|---|---|---|---|---|\n")

	for i := range r.Profiles {
		p := &r.Profiles[i]

		for j := range p.Stores {
			s := &p.Stores[j]

			if s.Sweep == nil {
				continue
			}

			w := s.Sweep
			fmt.Fprintf(b, "| %s | %s | %d | %.0f | %.1f | %.0f | %.0f |\n",
				p.Profile.Name, s.Store, w.Deleted, w.ListMs, w.KeysMs, w.DeletesMs, w.TotalMs)
		}
	}

	b.WriteString("\n")
}

// writeLimits writes the limit sweep.
func writeLimits(b *strings.Builder, r *results) {
	if len(r.Limits) == 0 {
		return
	}

	b.WriteString("## Limit sweep (p50 / p99 ms)\n\n" +
		"| Step | Store | DB ping | OpenTofu state upload | Pulumi checkpoint save | Update cycle | " +
		"Backup (ms) | Restore (ms) |\n|---|---|---|---|---|---|---|---|\n")

	for i := range r.Limits {
		l := &r.Limits[i]

		if l.Load == nil || l.Load.Report == nil {
			fmt.Fprintf(b, "| %s | %s | %s | run failed: %s | | | | |\n",
				l.Profile.Name, l.Store, pair(l.DBPing), oneLine(loadError(l.Load)))

			continue
		}

		rep := l.Load.Report
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", l.Profile.Name, l.Store, pair(l.DBPing),
			flowPair(rep.StateUpload), flowPair(rep.CheckpointSave), flowPair(rep.UpdateCycle),
			operation(rep.Backup), operation(rep.Restore))
	}

	b.WriteString("\n")
}

// loadError answers the error of a load result, or a note that the run
// left no report and no log.
func loadError(l *loadResult) string {
	if l == nil {
		return "no report and no log"
	}

	return l.Error
}

// oneLine answers s without line breaks and table bars, cut to 160 bytes.
func oneLine(s string) string {
	const maxCell = 160

	s = strings.NewReplacer("\n", " ", "\r", " ", "|", "/").Replace(s)
	if len(s) > maxCell {
		s = s[:maxCell] + "..."
	}

	return s
}
