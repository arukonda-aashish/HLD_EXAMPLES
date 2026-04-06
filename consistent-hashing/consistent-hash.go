package consistenthashing

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

var (
	ErrNoNodes         = errors.New("consistenthash: no nodes in ring")
	ErrNodeNotFound    = errors.New("consistenthash: node not found")
	ErrNodeExists      = errors.New("consistenthash: node already exists")
	ErrInvalidReplicas = errors.New("consistenthash: replication factor exceeds node count")
	ErrInvalidWeight   = errors.New("consistenthash: weight must be > 0")
)

type NodeStatus uint8

const (
	NodeStatusUp NodeStatus = iota
	NodeStatusDraining
	NodeStatusDown
)

func (s NodeStatus) String() string {
	switch s {
	case NodeStatusUp:
		return "UP"
	case NodeStatusDraining:
		return "DRAINING"
	case NodeStatusDown:
		return "DOWN"
	default:
		return "UNKNOWN"
	}
}

type Node struct {
	ID       string
	Addr     string
	Weight   int
	Status   NodeStatus
	Tags     map[string]string
	JoinedAt time.Time
}

func (n *Node) virtualCount(baseVnodes int) int {
	w := n.Weight
	if w <= 0 {
		w = 1
	}
	return baseVnodes * w
}

type virtualNode struct {
	hash     uint64
	nodeID   string
	vnodeIdx int
}

type HashFn func(data []byte) uint64

func defaultHash(data []byte) uint64 {
	h := sha256.Sum256(data)
	return binary.BigEndian.Uint64(h[:8])
}

type Config struct {
	BaseVirtualNodes  int
	ReplicationFactor int
	HashFn            HashFn
	LoadFactor        float64
}

func (c *Config) defaults() {
	if c.BaseVirtualNodes <= 0 {
		c.BaseVirtualNodes = 150
	}
	if c.ReplicationFactor <= 0 {
		c.ReplicationFactor = 1
	}
	if c.HashFn == nil {
		c.HashFn = defaultHash
	}
	if c.LoadFactor < 0 {
		c.LoadFactor = 0
	}
}

type Ring struct {
	mu     sync.RWMutex
	cfg    Config
	nodes  map[string]*Node
	vnodes []virtualNode
	loads  map[string]int64
}

func New(cfg Config) *Ring {
	cfg.defaults()
	return &Ring{
		cfg:   cfg,
		nodes: make(map[string]*Node),
		loads: make(map[string]int64),
	}
}

func (r *Ring) AddNode(n Node) error {
	if n.Weight <= 0 {
		return ErrInvalidWeight
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[n.ID]; exists {
		return ErrNodeExists
	}

	if n.JoinedAt.IsZero() {
		n.JoinedAt = time.Now().UTC()
	}
	if n.Tags == nil {
		n.Tags = make(map[string]string)
	}
	r.nodes[n.ID] = &n
	r.loads[n.ID] = 0
	r.addVnodes(&n)
	return nil

}

func (r *Ring) RemoveNode(nodeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[nodeID]; !exists {
		return ErrNodeNotFound
	}
	delete(r.nodes, nodeID)
	delete(r.loads, nodeID)
	r.rebuildRing()
	return nil
}

func (r *Ring) SetStatus(nodeID string, status NodeStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, exists := r.nodes[nodeID]

	if !exists {
		return ErrNodeNotFound
	}

	n.Status = status
	return nil
}

func (r *Ring) UpdateWeight(nodeID string, weight int) error {
	if weight <= 0 {
		return ErrInvalidWeight
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	n, exists := r.nodes[nodeID]

	if !exists {
		return ErrNodeNotFound
	}
	n.Weight = weight
	r.rebuildRing()
	return nil
}

func (r *Ring) Get(key string) (*Node, error) {
	nodes, err := r.GetN(key, 1)
	if err != nil {
		return nil, err
	}
	return nodes[0], nil
}

func (r *Ring) GetN(key string, n int) ([]*Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.nodes) == 0 {
		return nil, ErrNoNodes
	}
	healthy := 0

	for _, nd := range r.nodes {
		if nd.Status != NodeStatusDown {
			healthy++
		}
	}
	if n > healthy {
		n = healthy
	}
	if n == 0 {
		return nil, ErrNoNodes
	}

	h := r.cfg.HashFn([]byte(key))
	idx := r.searchRing(h)

	seen := make(map[string]struct{}, n)
	result := make([]*Node, 0, n)

	for i := 0; i < len(r.vnodes) && len(result) < n; i++ {
		vn := r.vnodes[(idx+i)%len(r.vnodes)]
		nd := r.nodes[vn.nodeID]
		if nd.Status == NodeStatusDown {
			continue
		}
		if _, dup := seen[vn.nodeID]; dup {
			continue
		}

		if r.cfg.LoadFactor > 0 {
			avgLoad := r.averageLoad()
			cap := int64(math.Ceil(avgLoad * r.cfg.LoadFactor))
			if cap > 0 && r.loads[vn.nodeID] >= cap {
				continue
			}
		}
		seen[vn.nodeID] = struct{}{}
		result = append(result, nd)
	}
	if len(result) == 0 {
		return nil, ErrNoNodes
	}
	return result, nil
}

func (r *Ring) RecordLoad(nodeID string, delta int64) {
	r.mu.Lock()
	r.loads[nodeID] += delta
	r.mu.Unlock()
}

func (r *Ring) Nodes() []*Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		cp := *n
		out = append(out, &cp)
	}
	return out
}

func (r *Ring) VNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.vnodes)
}
func (r *Ring) Distribution() map[string]float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.vnodes) == 0 {
		return nil
	}

	counts := make(map[string]int, len(r.nodes))
	for _, vn := range r.vnodes {
		counts[vn.nodeID]++
	}

	total := float64(len(r.vnodes))
	dist := make(map[string]float64, len(counts))
	for id, c := range counts {
		dist[id] = float64(c) / total * 100
	}
	return dist
}

func (r *Ring) Loads() map[string]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make(map[string]int64, len(r.loads))
	for k, v := range r.loads {
		out[k] = v
	}
	return out
}

func (r *Ring) addVnodes(n *Node) {
	count := n.virtualCount(r.cfg.BaseVirtualNodes)
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%s#vn%d", n.ID, i)
		h := r.cfg.HashFn([]byte(key))
		r.vnodes = append(r.vnodes, virtualNode{
			hash:     h,
			nodeID:   n.ID,
			vnodeIdx: i,
		})

	}
	sort.Slice(r.vnodes, func(i, j int) bool {
		return r.vnodes[i].hash < r.vnodes[j].hash
	})
}

func (r *Ring) rebuildRing() {
	r.vnodes = r.vnodes[:0]
	for _, n := range r.nodes {
		r.addVnodes(n)
	}
}

func (r *Ring) searchRing(h uint64) int {
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].hash >= h
	})
	return idx % len(r.vnodes)
}

func (r *Ring) averageLoad() float64 {
	if len(r.nodes) == 0 {
		return 0
	}
	var total int64
	var count int
	for id, nd := range r.nodes {
		if nd.Status != NodeStatusDown {
			total += r.loads[id]
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}
