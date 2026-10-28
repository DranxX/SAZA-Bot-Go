package lib

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── FlightCache ──────────────────────────────────────────────────────────────

func TestFlightCacheDedupesConcurrentCalls(t *testing.T) {
	fc := NewFlightCache()
	var calls atomic.Int64
	release := make(chan struct{})
	fn := func() (any, error) {
		calls.Add(1)
		<-release
		return "val", nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := fc.Do("k1", time.Minute, fn)
			if err != nil || v != "val" {
				t.Errorf("Do = %v, %v", v, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond) // let goroutines pile onto the same flight
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

func TestFlightCacheTTL(t *testing.T) {
	fc := NewFlightCache()
	calls := 0
	fn := func() (any, error) { calls++; return calls, nil }

	if v, _ := fc.Do("k", 30*time.Millisecond, fn); v != 1 {
		t.Fatalf("first Do = %v", v)
	}
	if v, _ := fc.Do("k", 30*time.Millisecond, fn); v != 1 {
		t.Fatalf("cached Do = %v, want 1", v)
	}
	time.Sleep(40 * time.Millisecond)
	if v, _ := fc.Do("k", 30*time.Millisecond, fn); v != 2 {
		t.Fatalf("expired Do = %v, want 2", v)
	}
}

func TestFlightCacheStaleFallback(t *testing.T) {
	fc := NewFlightCache()
	if v, _ := fc.Do("k", 10*time.Millisecond, func() (any, error) { return "good", nil }); v != "good" {
		t.Fatalf("first Do = %v", v)
	}
	time.Sleep(20 * time.Millisecond)
	v, err := fc.Do("k", 10*time.Millisecond, func() (any, error) { return nil, errors.New("down") })
	if err != nil || v != "good" {
		t.Fatalf("stale fallback = (%v, %v), want (good, nil)", v, err)
	}
}

func TestFlightCacheDedupeOnlyMode(t *testing.T) {
	fc := NewFlightCache()
	calls := 0
	fn := func() (any, error) { calls++; return calls, nil }
	_, _ = fc.Do("k", 0, fn)
	_, _ = fc.Do("k", 0, fn) // ttl=0: never cached, always fetched
	if calls != 2 {
		t.Fatalf("dedupe-only mode fetched %d times, want 2", calls)
	}
}

// ── AntiSpam ─────────────────────────────────────────────────────────────────

var (
	asCfg       = AntiSpamConfig{WindowMs: 30_000, MaxWarn: 2, MaxBan: 4}
	asOneSecCfg = AntiSpamConfig{WindowMs: 1_000, MaxWarn: 4, MaxBan: 5}
)

func TestAntiSpamWarnThenBan(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	if d := a.Check("u1", base, asCfg); !d.Allowed {
		t.Fatalf("n=1 decision = %+v, want allowed", d)
	}
	d := a.Check("u1", base+100, asCfg) // n=2 == MaxWarn
	if d.Allowed || !d.Warn || d.Ban {
		t.Fatalf("n=2 decision = %+v, want blocked+warn", d)
	}
	if a.Check("u1", base+200, asCfg).Warn {
		t.Fatal("warn must fire only once per window")
	}
	d = a.Check("u1", base+300, asCfg) // n=4 >= MaxBan
	if d.Allowed || !d.Ban {
		t.Fatalf("n=4 decision = %+v, want ban", d)
	}
}

func TestAntiSpamPerContact(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	a.Check("u2", base, asCfg)
	a.Check("u2", base+10, asCfg)
	if d := a.Check("u3", base+20, asCfg); !d.Allowed || d.Count != 1 {
		t.Fatalf("other contact decision = %+v", d)
	}
}

func TestAntiSpamSweepAndReset(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	a.Check("u4", base, asCfg)
	a.Check("u4", base+10, asCfg)
	a.Reset("u4")
	if d := a.Check("u4", base+20, asCfg); !d.Allowed || d.Count != 1 {
		t.Fatalf("after reset decision = %+v", d)
	}
	a.Check("u5", base+30, asCfg)
	a.mu.Lock()
	a.hits["old"] = []int64{time.Now().UnixMilli() - 100_000}
	a.mu.Unlock()
	a.Sweep(30_000)
	a.mu.Lock()
	_, oldExists := a.hits["old"]
	_, u5exists := a.hits["u5"]
	a.mu.Unlock()
	if oldExists {
		t.Fatal("sweep should drop an idle contact")
	}
	if !u5exists {
		t.Fatal("sweep should keep a contact from the current window")
	}
}

// Regression: redelivered messages carry old timestamps and must never count
// into the current window — that burst is what temp-banned a first chat.
func TestAntiSpamOldTimestampsStayOutsideWindow(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	for i := 0; i < 4; i++ {
		a.Check("u", base-60_000+int64(i*100), asOneSecCfg)
	}
	if d := a.Check("u", base, asOneSecCfg); !d.Allowed || d.Count != 1 {
		t.Fatalf("old hits leaked into the window: %+v", d)
	}
}

// Spec: 5 commands in a row within 1 second → warn+drop on the 4th, tempban on the 5th.
func TestAntiSpamFiveInOneSecondBans(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		if d := a.Check("u", base+int64(i*100), asOneSecCfg); !d.Allowed {
			t.Fatalf("hit %d blocked before the limit: %+v", i+1, d)
		}
	}
	d := a.Check("u", base+300, asOneSecCfg) // 4th within 1s → warn + drop
	if d.Allowed || !d.Warn || d.Ban {
		t.Fatalf("4th hit = %+v, want blocked+warn", d)
	}
	d = a.Check("u", base+400, asOneSecCfg) // 5th within 1s → ban
	if d.Allowed || !d.Ban {
		t.Fatalf("5th hit = %+v, want ban", d)
	}
}

// Spec: after the window passes, counting restarts from zero.
func TestAntiSpamCounterResetsAfterWindow(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	for i := 0; i < 4; i++ {
		a.Check("u", base+int64(i*100), asOneSecCfg)
	}
	if d := a.Check("u", base+1_500, asOneSecCfg); !d.Allowed || d.Count != 1 {
		t.Fatalf("post-window hit = %+v, want allowed with count 1", d)
	}
}

// ── MsgDedup ─────────────────────────────────────────────────────────────────

func TestMsgDedupDuplicates(t *testing.T) {
	d := NewMsgDedup(30_000)
	if d.Seen("chat\x00id1") {
		t.Fatal("first sight reported as duplicate")
	}
	if !d.Seen("chat\x00id1") {
		t.Fatal("repeat not reported as duplicate")
	}
	if d.Seen("chat\x00id2") || d.Seen("other\x00id1") {
		t.Fatal("different chat or id reported as duplicate")
	}
	if d.Seen("") {
		t.Fatal("empty key must never be tracked")
	}
}

func TestMsgDedupTTL(t *testing.T) {
	d := NewMsgDedup(30)
	if d.Seen("k") || !d.Seen("k") {
		t.Fatal("duplicate not detected inside the TTL")
	}
	time.Sleep(40 * time.Millisecond)
	if d.Seen("k") {
		t.Fatal("key still reported after the TTL")
	}
}

func TestMsgDedupBounded(t *testing.T) {
	d := NewMsgDedup(30_000)
	for i := 0; i < 10_000; i++ {
		d.Seen(strconv.Itoa(i))
	}
	d.mu.Lock()
	n := len(d.seen)
	d.mu.Unlock()
	if n > 8192 {
		t.Fatalf("dedup map grew to %d entries, want <= 8192", n)
	}
}

func TestMsgDedupClampsNonPositiveTTL(t *testing.T) {
	d := NewMsgDedup(0)
	if d.Seen("k") {
		t.Fatal("first sight reported as duplicate")
	}
	if !d.Seen("k") {
		t.Fatal("zero ttl must fall back to a working dedup, not no-op")
	}
}

func TestMsgDedupConcurrent(t *testing.T) {
	d := NewMsgDedup(30_000)
	var wg sync.WaitGroup
	var first atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !d.Seen("k") {
				first.Add(1)
			}
		}()
	}
	wg.Wait()
	if first.Load() != 1 {
		t.Fatalf("concurrent first-sight count = %d, want exactly 1", first.Load())
	}
}

func TestAntiSpamConcurrent(t *testing.T) {
	a := NewAntiSpam()
	base := time.Now().UnixMilli()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Check("u", base, asOneSecCfg)
		}()
	}
	wg.Wait()
	a.mu.Lock()
	n := len(a.hits["u"])
	a.mu.Unlock()
	if n != 20 {
		t.Fatalf("recorded %d hits, want 20 (no lost updates)", n)
	}
}

// ── CommandGate ──────────────────────────────────────────────────────────────

func TestGateFreeSerial(t *testing.T) {
	g := NewCommandGate(1, 2)
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	p1 := make(chan error, 1)
	go func() {
		p1 <- g.Run(context.Background(), "u1", true, func() error { started <- struct{}{}; <-release; return nil })
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first command never started")
	}

	p2 := make(chan error, 1)
	go func() {
		p2 <- g.Run(context.Background(), "u1", true, func() error { started <- struct{}{}; return nil })
	}()

	select {
	case <-started:
		t.Fatal("second command started while the first was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-p1; err != nil {
		t.Fatalf("first: %v", err)
	}
	select {
	case err := <-p2:
		if err != nil {
			t.Fatalf("second: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second command never ran after release")
	}
}

func TestGatePremiumParallel(t *testing.T) {
	g := NewCommandGate(1, 2)
	release := make(chan struct{})
	bothEntered := make(chan struct{})
	var entered atomic.Int32

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Run(context.Background(), "u2", false, func() error {
				if entered.Add(1) == 2 {
					close(bothEntered)
				}
				<-release
				return nil
			})
		}()
	}

	select {
	case <-bothEntered:
	case <-time.After(time.Second):
		t.Fatal("premium commands did not run in parallel")
	}
	close(release)
	wg.Wait()
}

func TestGateQueueFull(t *testing.T) {
	g := NewCommandGate(1, 0) // maxQueue 0 → reject
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background(), "u3", true, func() error { <-release; return nil }) }()
	time.Sleep(30 * time.Millisecond)

	if err := g.Run(context.Background(), "u3", true, func() error { return nil }); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("overflow error = %v, want ErrQueueFull", err)
	}
	close(release)
	<-done
}

func TestGateCtxTimeout(t *testing.T) {
	g := NewCommandGate(1, 2)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background(), "u4", true, func() error { <-release; return nil }) }()
	time.Sleep(30 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := g.Run(ctx, "u4", true, func() error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want DeadlineExceeded", err)
	}
	close(release)
	<-done
}

func TestGateDifferentContactsParallel(t *testing.T) {
	g := NewCommandGate(1, 2)
	release := make(chan struct{})
	bothEntered := make(chan struct{})
	var entered atomic.Int32

	var wg sync.WaitGroup
	for _, contact := range []string{"u5", "u6"} {
		wg.Add(1)
		go func(c string) {
			defer wg.Done()
			_ = g.Run(context.Background(), c, true, func() error {
				if entered.Add(1) == 2 {
					close(bothEntered)
				}
				<-release
				return nil
			})
		}(contact)
	}

	select {
	case <-bothEntered:
	case <-time.After(time.Second):
		t.Fatal("different contacts did not run in parallel")
	}
	close(release)
	wg.Wait()
}
