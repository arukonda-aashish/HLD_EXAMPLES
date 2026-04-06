package consistenthashing

import (
	"fmt"
	"math"
	"sync"
	"testing"

	ch "github.com/arukonda-aashish/consistent-hashing"
)

// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

func newRing(baseVnodes int) *ch.Ring {
	return ch.New(ch.Config{
		BaseVirtualNodes:  baseVnodes,
		ReplicationFactor: 3,
	})
}

func mustAdd(t *testing.T, r *ch.Ring, id string, weight int) {
	t.Helper()
	err := r.AddNode(ch.Node{ID: id, Addr: id + ":6379", Weight: weight})
	if err != nil {
		t.Fatalf("AddNode(%s): %v", id, err)
	}
}

// ─────────────────────────────────────────────
//  Basic correctness
// ─────────────────────────────────────────────

func TestGetEmptyRing(t *testing.T) {
	r := newRing(100)
	_, err := r.Get("any-key")
	if err != ch.ErrNoNodes {
		t.Fatalf("expected ErrNoNodes, got %v", err)
	}
}

func TestAddAndGet(t *testing.T) {
	r := newRing(100)
	mustAdd(t, r, "node-1", 1)

	node, err := r.Get("some-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if node.ID != "node-1" {
		t.Fatalf("expected node-1, got %s", node.ID)
	}
}

func TestDuplicateAdd(t *testing.T) {
	r := newRing(100)
	mustAdd(t, r, "node-1", 1)
	err := r.AddNode(ch.Node{ID: "node-1", Addr: "node-1:6379", Weight: 1})
	if err != ch.ErrNodeExists {
		t.Fatalf("expected ErrNodeExists, got %v", err)
	}
}

func TestRemoveNode(t *testing.T) {
	r := newRing(150)
	mustAdd(t, r, "A", 1)
	mustAdd(t, r, "B", 1)
	mustAdd(t, r, "C", 1)

	// Map 1000 keys before removal.
	before := make(map[string]string, 1000)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		n, _ := r.Get(key)
		before[key] = n.ID
	}

	if err := r.RemoveNode("B"); err != nil {
		t.Fatal(err)
	}

	// After removing B, keys that mapped to B must remap; others must not move.
	moved := 0
	for key, old := range before {
		n, _ := r.Get(key)
		if n.ID != old {
			moved++
			// Must only move FROM B — not between A and C.
			if old != "B" {
				t.Errorf("key %s moved from %s to %s (should only move from B)", key, old, n.ID)
			}
		}
	}
	t.Logf("%d / 1000 keys migrated after removing B", moved)
}

func TestRemoveNonExistent(t *testing.T) {
	r := newRing(100)
	if err := r.RemoveNode("ghost"); err != ch.ErrNodeNotFound {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}

// ─────────────────────────────────────────────
//  Consistent remapping
// ─────────────────────────────────────────────

// When we ADD a new node, only ~1/N of keys should move.
func TestMinimalDisruptionOnAdd(t *testing.T) {
	const N = 5
	const keys = 10_000

	r := newRing(150)
	for i := 1; i <= N; i++ {
		mustAdd(t, r, fmt.Sprintf("node-%d", i), 1)
	}

	before := make(map[string]string, keys)
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("k-%d", i)
		n, _ := r.Get(key)
		before[key] = n.ID
	}

	mustAdd(t, r, "node-new", 1)

	moved := 0
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("k-%d", i)
		n, _ := r.Get(key)
		if n.ID != before[key] {
			moved++
		}
	}

	pct := float64(moved) / keys * 100
	expected := 100.0 / float64(N+1)
	t.Logf("keys moved: %d / %d  (%.1f%%, ideal %.1f%%)", moved, keys, pct, expected)

	// Allow ±50% variance around the ideal.
	if pct > expected*1.5 {
		t.Errorf("too many keys moved: %.1f%% > 1.5× ideal %.1f%%", pct, expected)
	}
}

// ─────────────────────────────────────────────
//  Distribution / balance
// ─────────────────────────────────────────────

// With 150 vnodes per node, no node should hold more than 2× its fair share.
func TestKeyDistribution(t *testing.T) {
	const nodes = 6
	const keys = 100_000
	const maxDeviation = 2.0 // 2× the ideal

	r := newRing(150)
	for i := 1; i <= nodes; i++ {
		mustAdd(t, r, fmt.Sprintf("n%d", i), 1)
	}

	counts := make(map[string]int)
	for i := 0; i < keys; i++ {
		n, _ := r.Get(fmt.Sprintf("key-%d", i))
		counts[n.ID]++
	}

	ideal := float64(keys) / nodes
	for id, c := range counts {
		ratio := float64(c) / ideal
		t.Logf("  %s: %d keys (%.2f× ideal)", id, c, ratio)
		if ratio > maxDeviation {
			t.Errorf("%s holds %.2f× ideal — distribution too skewed", id, ratio)
		}
	}
}

// ─────────────────────────────────────────────
//  Weighted nodes
// ─────────────────────────────────────────────

func TestWeightedDistribution(t *testing.T) {
	const keys = 100_000

	r := newRing(100)
	mustAdd(t, r, "heavy", 3) // should get ~3× the keys
	mustAdd(t, r, "light", 1)

	counts := make(map[string]int)
	for i := 0; i < keys; i++ {
		n, _ := r.Get(fmt.Sprintf("k-%d", i))
		counts[n.ID]++
	}

	ratio := float64(counts["heavy"]) / float64(counts["light"])
	t.Logf("heavy=%d  light=%d  ratio=%.2f (ideal 3.0)", counts["heavy"], counts["light"], ratio)

	if ratio < 2.0 || ratio > 4.5 {
		t.Errorf("expected ratio near 3.0, got %.2f", ratio)
	}
}

// ─────────────────────────────────────────────
//  Replication / GetN
// ─────────────────────────────────────────────

func TestGetNReturnsDistinctNodes(t *testing.T) {
	r := newRing(150)
	for i := 1; i <= 5; i++ {
		mustAdd(t, r, fmt.Sprintf("node-%d", i), 1)
	}

	nodes, err := r.GetN("repl-key", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	seen := make(map[string]bool)
	for _, n := range nodes {
		if seen[n.ID] {
			t.Errorf("duplicate node in preference list: %s", n.ID)
		}
		seen[n.ID] = true
	}
}

func TestGetNFewerNodesThanRequested(t *testing.T) {
	r := newRing(150)
	mustAdd(t, r, "solo", 1)

	nodes, err := r.GetN("key", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 (all healthy), got %d", len(nodes))
	}
}

// ─────────────────────────────────────────────
//  Node status / fault tolerance
// ─────────────────────────────────────────────

func TestDownNodeSkipped(t *testing.T) {
	r := newRing(150)
	mustAdd(t, r, "A", 1)
	mustAdd(t, r, "B", 1)
	mustAdd(t, r, "C", 1)

	// Mark B as down.
	if err := r.SetStatus("B", ch.NodeStatusDown); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000; i++ {
		n, err := r.Get(fmt.Sprintf("key-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if n.ID == "B" {
			t.Error("Got a Down node: B")
		}
	}
}

func TestAllNodesDown(t *testing.T) {
	r := newRing(150)
	mustAdd(t, r, "A", 1)
	_ = r.SetStatus("A", ch.NodeStatusDown)

	_, err := r.Get("key")
	if err != ch.ErrNoNodes {
		t.Fatalf("expected ErrNoNodes when all nodes are down, got %v", err)
	}
}

// ─────────────────────────────────────────────
//  Bounded load
// ─────────────────────────────────────────────

func TestBoundedLoad(t *testing.T) {
	r := ch.New(ch.Config{
		BaseVirtualNodes:  150,
		ReplicationFactor: 1,
		LoadFactor:        1.25,
	})
	mustAdd(t, r, "A", 1)
	mustAdd(t, r, "B", 1)
	mustAdd(t, r, "C", 1)

	counts := make(map[string]int)
	for i := 0; i < 3000; i++ {
		key := fmt.Sprintf("bk-%d", i)
		n, err := r.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		counts[n.ID]++
		r.RecordLoad(n.ID, 1)
	}

	for id, c := range counts {
		t.Logf("  %s: %d keys", id, c)
	}

	// Each node should hold at most 125% of the ideal (3000/3 = 1000).
	ideal := 1000.0
	for id, c := range counts {
		if float64(c) > ideal*1.25+50 { // +50 tolerance for small N
			t.Errorf("%s over bounded load: %d (ideal %.0f)", id, c, ideal)
		}
	}
}

// ─────────────────────────────────────────────
//  Concurrency
// ─────────────────────────────────────────────

func TestConcurrentReadsAndWrites(t *testing.T) {
	r := newRing(100)
	for i := 1; i <= 4; i++ {
		mustAdd(t, r, fmt.Sprintf("node-%d", i), 1)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 1000)

	// Concurrent readers
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_, err := r.Get(fmt.Sprintf("k-%d-%d", id, j))
				if err != nil {
					errs <- err
				}
			}
		}(i)
	}

	// Concurrent writers (add/remove a transient node)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("transient-%d", id)
			_ = r.AddNode(ch.Node{ID: nodeID, Addr: nodeID + ":6379", Weight: 1})
			_ = r.RemoveNode(nodeID)
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

// ─────────────────────────────────────────────
//  Distribution report (not a failure test)
// ─────────────────────────────────────────────

func TestDistributionReport(t *testing.T) {
	r := newRing(150)
	for i := 1; i <= 5; i++ {
		mustAdd(t, r, fmt.Sprintf("n%d", i), 1)
	}

	dist := r.Distribution()
	for id, pct := range dist {
		t.Logf("  %s owns %.2f%% of ring", id, pct)
	}

	// Standard deviation across nodes should be < 5% for 150 vnodes.
	vals := make([]float64, 0, len(dist))
	var mean float64
	for _, pct := range dist {
		vals = append(vals, pct)
		mean += pct
	}
	mean /= float64(len(vals))

	var variance float64
	for _, v := range vals {
		d := v - mean
		variance += d * d
	}
	stddev := math.Sqrt(variance / float64(len(vals)))
	t.Logf("  stddev across nodes: %.2f%%", stddev)
	if stddev > 5.0 {
		t.Errorf("ring imbalance too high: stddev %.2f%% > 5%%", stddev)
	}
}

// ─────────────────────────────────────────────
//  Custom hash function
// ─────────────────────────────────────────────

func TestCustomHashFn(t *testing.T) {
	// Use a trivial hash to verify pluggability.
	trivialHash := func(data []byte) uint64 {
		var h uint64
		for _, b := range data {
			h = h*31 + uint64(b)
		}
		return h
	}

	r := ch.New(ch.Config{
		BaseVirtualNodes: 100,
		HashFn:           trivialHash,
	})
	mustAdd(t, r, "x", 1)
	mustAdd(t, r, "y", 1)

	n, err := r.Get("hello")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("key 'hello' → %s (with custom hash)", n.ID)
}

// ─────────────────────────────────────────────
//  Benchmark
// ─────────────────────────────────────────────

func BenchmarkGet(b *testing.B) {
	r := ch.New(ch.Config{BaseVirtualNodes: 150})
	for i := 1; i <= 10; i++ {
		_ = r.AddNode(ch.Node{ID: fmt.Sprintf("node-%d", i), Addr: fmt.Sprintf("10.0.0.%d:6379", i), Weight: 1})
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = r.Get(fmt.Sprintf("key-%d", i))
			i++
		}
	})
}

func BenchmarkGetN(b *testing.B) {
	r := ch.New(ch.Config{BaseVirtualNodes: 150, ReplicationFactor: 3})
	for i := 1; i <= 10; i++ {
		_ = r.AddNode(ch.Node{ID: fmt.Sprintf("node-%d", i), Addr: fmt.Sprintf("10.0.0.%d:6379", i), Weight: 1})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.GetN(fmt.Sprintf("key-%d", i), 3)
	}
}
