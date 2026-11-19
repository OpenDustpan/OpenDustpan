package trustrulesvc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// maxGuardKeyBytes is the longest limiter key that the guard keeps as it is.
// A longer key becomes its digest, so no key costs more than this in memory.
const maxGuardKeyBytes = 64

// MtlsGuardConfig holds the settings of an MtlsGuard. Each number must be
// positive.
type MtlsGuardConfig struct {
	// MaxBodyBytes is the largest request body that the guard admits.
	MaxBodyBytes int64
	// Limit is the number of calls that one key makes in one window.
	Limit int
	// GlobalLimit is the number of calls of all keys in one window.
	GlobalLimit int
	// Window is the length of a rate window.
	Window time.Duration
	// MaxKeys bounds the number of keys that the guard holds. A key beyond
	// the bound shares the one overflow key, so the memory of the guard has
	// a fixed upper bound, also for calls that no sign-in came before.
	MaxKeys int
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// MtlsGuard limits the calls of the mTLS exchange before any other work: the
// size of the body, and the rate of the calls of each key and of all keys.
// The guard is safe for concurrent use.
type MtlsGuard struct {
	cfg MtlsGuardConfig
	now func() time.Time

	mu          sync.Mutex
	keys        map[string]*guardWindow
	overflow    guardWindow
	global      guardWindow
	lastCleanup time.Time
	// carry holds the counts of windows that are over, by namespace name. It
	// has at most maxRefusalNames+1 entries: the name "" is the sum of all
	// other names.
	carry map[string]int
}

// maxRefusalNames is the number of namespace names that one window of the
// guard tracks for the first refusal. A name beyond the bound shares one
// slot, so the memory of a window has a fixed upper bound.
const maxRefusalNames = 8

// refusalSlot is the state of the refusals of one namespace name in one
// window. marked is true after the guard reported the first refusal and the
// caller did not give it back. held is the number of refusals since then,
// which the guard counted and which wrote no entry.
type refusalSlot struct {
	name   string
	marked bool
	held   int
}

// guardWindow counts the calls since start. named and shared hold the first
// refusal state of each namespace name of the window. freed is the number of
// slots that the caller gave back for a namespace that does not exist, which
// the guard bounds so that the reads for such names stay bounded.
type guardWindow struct {
	start  time.Time
	count  int
	named  []refusalSlot
	shared refusalSlot
	freed  int
}

// HeldCount is a number of refused calls to a namespace that the guard
// counted and that wrote no entry. An empty Namespace is the sum for the
// names beyond the bound of the guard.
type HeldCount struct {
	Namespace string
	Count     int
}

// Refusal is the result of a refused Admit. The caller gives it back to
// Rearm when the denial entry fails.
type Refusal struct {
	// First is true when the caller must write the denial entry of this
	// refusal. It is true at most once for each window and namespace name,
	// until the caller gives the refusal back.
	First bool
	// Held holds the counts of refused calls that earlier windows left
	// without an entry. The caller writes them after the entry of this
	// refusal, and gives back the ones that it could not write.
	Held []HeldCount

	win   *guardWindow
	start time.Time
	name  string
}

// NewMtlsGuard returns an MtlsGuard, or trustrule.ErrMissingDependency for a
// config with a number that is not positive.
func NewMtlsGuard(cfg MtlsGuardConfig) (*MtlsGuard, error) {
	if cfg.MaxBodyBytes < 0 || cfg.Limit < 1 || cfg.GlobalLimit < 1 || cfg.Window <= 0 || cfg.MaxKeys < 1 {
		return nil, fmt.Errorf("%w: the guard needs a body cap that is not negative, and a rate limit, a global "+
			"limit, a window and a key bound that are positive", trustrule.ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	cfg.Now = now

	return &MtlsGuard{
		cfg:         cfg,
		now:         now,
		mu:          sync.Mutex{},
		keys:        make(map[string]*guardWindow),
		overflow:    newGuardWindow(),
		global:      newGuardWindow(),
		lastCleanup: time.Time{},
		carry:       make(map[string]int),
	}, nil
}

// newGuardWindow returns an empty window.
func newGuardWindow() guardWindow {
	return guardWindow{
		start: time.Time{}, count: 0, named: nil, shared: refusalSlot{name: "", marked: false, held: 0}, freed: 0,
	}
}

// Admit counts one call of the key and checks it. It returns ErrBodyTooLarge
// when bodyLen is over the cap or negative, and ErrRateLimited when the key,
// or all keys together, made too many calls in the window. A call with no
// certificate counts too: it is the call that needs no work of the server and
// the one that a flood sends. The key is an address of the caller: it is not
// trusted, so the guard bounds the memory that keys take.
//
// namespace is the name of the namespace of the call. The first refusal is
// reported for each window and namespace name, so a refusal to a namespace
// that does not exist never hides the refusals of another one. A window
// tracks at most maxRefusalNames names. The refusal that Admit returns with an
// error says if the caller must write the denial entry (First). While all
// keys together are over the limit, the window of all keys is the one that
// counts, so a flood from many keys also gives one first refusal for each
// window and name. A refusal counts in memory when it is not first, and
// Held returns the count of a window that is over, with the first refusal of
// a later window. The Refusal of a call that Admit admits is empty.
func (g *MtlsGuard) Admit(key, namespace string, bodyLen int64) (Refusal, error) {
	if len(key) > maxGuardKeyBytes {
		sum := sha256.Sum256([]byte(key))
		key = hex.EncodeToString(sum[:])
	}

	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()

	keyWindow := g.windowOf(key, now)
	globalOver := g.count(&g.global, now) > g.cfg.GlobalLimit
	keyOver := g.count(keyWindow, now) > g.cfg.Limit

	var err error

	switch {
	case bodyLen < 0 || bodyLen > g.cfg.MaxBodyBytes:
		err = fmt.Errorf("%w: %d bytes, at most %d", ErrBodyTooLarge, bodyLen, g.cfg.MaxBodyBytes)
	case globalOver || keyOver:
		err = ErrRateLimited
	default:
		return Refusal{First: false, Held: nil, win: nil, start: time.Time{}, name: ""}, nil
	}

	w := keyWindow
	if globalOver {
		w = &g.global
	}

	slot, name := w.slotFor(namespace)
	r := Refusal{First: false, Held: nil, win: w, start: w.start, name: name}

	if slot.marked {
		slot.held++

		return r, err
	}

	slot.marked = true
	r.First = true
	r.Held = g.takeCarry()

	return r, err
}

// Rearm gives a first refusal back, because the caller could not write its
// denial entry: the next refusal of the window and name is a first refusal
// again, and the counts of r return to the guard.
func (g *MtlsGuard) Rearm(r Refusal) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.returnCarry(r.Held)

	if slot := g.slotOf(r); slot != nil {
		slot.marked = false
	}
}

// Forget gives a first refusal back, because its namespace does not exist
// and so has no audit log. The guard frees the slot of the name, so a name
// that does not exist never takes the slot of a real namespace. It does this
// at most maxRefusalNames times for each window, so the reads for names that
// do not exist stay bounded. After that, the slot stays marked.
func (g *MtlsGuard) Forget(r Refusal) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.returnCarry(r.Held)

	if g.slotOf(r) == nil || r.win.freed >= maxRefusalNames {
		return
	}

	r.win.freed++
	r.win.drop(r.name)
}

// GiveBack returns counts that the caller could not write to the guard, so
// that the next first refusal returns them again.
func (g *MtlsGuard) GiveBack(held []HeldCount) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.returnCarry(held)
}

// slotFor returns the slot of name in w, and the name that the slot has: ""
// for the shared slot. It makes the slot when the window has room. A name
// that is too long or beyond the bound uses the shared slot.
func (w *guardWindow) slotFor(name string) (*refusalSlot, string) {
	if slot, found := w.findSlot(name); found {
		return slot, slot.name
	}

	if len(w.named) >= maxRefusalNames || len(name) > maxGuardKeyBytes {
		return &w.shared, ""
	}

	w.named = append(w.named, refusalSlot{name: name, marked: false, held: 0})

	return &w.named[len(w.named)-1], name
}

// findSlot returns the slot of name in w, and false when the window has no
// slot for it. The shared slot has the name "".
func (w *guardWindow) findSlot(name string) (*refusalSlot, bool) {
	if name == "" {
		return &w.shared, true
	}

	for i := range w.named {
		if w.named[i].name == name {
			return &w.named[i], true
		}
	}

	return nil, false
}

// drop removes the named slot of name from w.
func (w *guardWindow) drop(name string) {
	for i := range w.named {
		if w.named[i].name == name {
			w.named = append(w.named[:i], w.named[i+1:]...)

			return
		}
	}

	if name == "" {
		w.shared.marked = false
	}
}

// Tracked returns the number of keys that the guard holds, for metrics and
// tests. It never passes MaxKeys.
func (g *MtlsGuard) Tracked() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return len(g.keys)
}

// count adds one call to w, after it starts a new window when the window of w
// is over, and returns the count of the window.
func (g *MtlsGuard) count(w *guardWindow, now time.Time) int {
	if w.start.IsZero() || !now.Before(w.start.Add(g.cfg.Window)) {
		g.fold(w)
		w.start, w.count, w.named, w.shared, w.freed = now, 0, nil, refusalSlot{name: "", marked: false, held: 0}, 0
	}

	w.count++

	return w.count
}

// windowOf returns the window of key. It holds at most MaxKeys windows. When
// the map is full, it first drops the windows that are over, at most once for
// each window of time, and a key that still finds no place shares the
// overflow window. The caller holds g.mu.
func (g *MtlsGuard) windowOf(key string, now time.Time) *guardWindow {
	if w, ok := g.keys[key]; ok {
		return w
	}

	if len(g.keys) >= g.cfg.MaxKeys && now.Sub(g.lastCleanup) >= g.cfg.Window {
		g.lastCleanup = now

		for k, w := range g.keys {
			if !now.Before(w.start.Add(g.cfg.Window)) {
				g.fold(w)
				delete(g.keys, k)
			}
		}
	}

	if len(g.keys) >= g.cfg.MaxKeys {
		return &g.overflow
	}

	fresh := newGuardWindow()
	w := &fresh
	g.keys[key] = w

	return w
}

// returnCarry adds held to the carry. The caller holds g.mu.
func (g *MtlsGuard) returnCarry(held []HeldCount) {
	for _, h := range held {
		g.addCarry(h.Namespace, h.Count)
	}
}

// addCarry adds n to the carry of name. A name that finds no place in the
// carry joins the sum of the other names. The caller holds g.mu.
func (g *MtlsGuard) addCarry(name string, n int) {
	if n < 1 {
		return
	}

	if _, ok := g.carry[name]; !ok && len(g.carry) > maxRefusalNames {
		name = ""
	}

	g.carry[name] += n
}

// takeCarry returns the carry and clears it. The caller holds g.mu.
func (g *MtlsGuard) takeCarry() []HeldCount {
	if len(g.carry) == 0 {
		return nil
	}

	out := make([]HeldCount, 0, len(g.carry))
	for name, n := range g.carry {
		out = append(out, HeldCount{Namespace: name, Count: n})
	}

	clear(g.carry)

	return out
}

// fold moves the held counts of w to the carry. The caller holds g.mu.
func (g *MtlsGuard) fold(w *guardWindow) {
	for i := range w.named {
		g.addCarry(w.named[i].name, w.named[i].held)
	}

	g.addCarry("", w.shared.held)
}

// slotOf returns the slot that r marked, or nil when its window is over or
// the slot is gone. The caller holds g.mu.
func (g *MtlsGuard) slotOf(r Refusal) *refusalSlot {
	if r.win == nil || !r.win.start.Equal(r.start) {
		return nil
	}

	slot, found := r.win.findSlot(r.name)
	if !found || !slot.marked {
		return nil
	}

	return slot
}
