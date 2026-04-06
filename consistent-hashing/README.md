# Consistent Hashing Library

This library implements a production-ready **consistent hashing system** with support for:

1. Core ring mechanics  
2. Virtual nodes and weighted capacity  
3. Replication / preference lists (GetN)  
4. Node status (Up / Draining / Down)  
5. Bounded-load / Load Factor  
6. Pluggable hash function  
7. Concurrency model  
8. Node addition and removal  

Inspired by distributed systems like Apache Cassandra, Amazon DynamoDB, and Redis Cluster.

---

## Table of Contents

1. [Core Ring Mechanics](#core-ring-mechanics)  
2. [Virtual Nodes and Weighted Capacity](#virtual-nodes-and-weighted-capacity)  
3. [Replication / Preference Lists (GetN)](#replication--preference-lists-getn)  
4. [Node Status (Up / Draining / Down)](#node-status-up--draining--down)  
5. [Bounded-Load / Load Factor](#bounded-load--load-factor)  
6. [Pluggable Hash Function](#pluggable-hash-function)  
7. [Concurrency Model](#concurrency-model)  
8. [Node Addition and Removal](#node-addition-and-removal)  

---

## Core Ring Mechanics

- Ring is a **sorted slice of virtual nodes**, each holding:
  - `hash` → `uint64` representing position
  - `nodeID` → identifier of physical node
- Key lookup:
  1. Compute `hash(key)`
  2. Binary search for the first vnode hash ≥ key hash
  3. Wrap around to first vnode if key hash > largest vnode hash
- Lookup complexity: **O(log N)**
- Minimal key movement occurs when nodes are added/removed

**Example:**

Ring hashes: `[3, 8, 14, 20]`

| Key Hash | Assigned Node |
|----------|---------------|
| 1        | 3             |
| 9        | 14            |
| 25       | 3             |

---

## Virtual Nodes and Weighted Capacity

- Each physical node has multiple vnodes:
num_vnodes = BaseVirtualNodes × Weight
- Virtual nodes hashed uniquely:
hash(nodeID + "#" + index)
- Weighted nodes get more key ranges → smooth distribution
- Handles heterogeneous hardware

**Example:**

| Node | Weight | Virtual Nodes |
|------|--------|---------------|
| S1   | 1      | 100           |
| S2   | 3      | 300           |

---

## Replication / Preference Lists (GetN)

- `GetN(key, N)` returns N distinct physical nodes:
  - First → primary coordinator
  - Remaining → replicas
- Skips duplicates of same physical node while moving clockwise
- Supports N-write / R-read quorum

**Example:**
GetN(key, 3) → [S2, S5, S1]

---

## Node Status (Up / Draining / Down)

- Node statuses:
  - **Up** → active  
  - **Draining** → graceful removal  
  - **Down** → failed/unavailable
- Lookups skip Down nodes automatically
- No need to rebuild ring on failure

---

## Bounded-Load / Load Factor

- Implements **Consistent Hashing with Bounded Loads**
- Ensures no node exceeds `LoadFactor × average load`
- Prevents hotspots with uneven key distribution
- Requires `RecordLoad(nodeID, delta)` after key assignment

**Example:**
- LoadFactor = 1.25 → max 125% of average load

---

## Pluggable Hash Function

- Supports injecting any hash function:
func([]byte) uint64
- Examples:
  - SHA-256 → secure, slower
  - MurmurHash3 → fast, good distribution
  - xxHash → extremely fast, weaker guarantees
- Determines uniformity and throughput

---

## Concurrency Model

- Uses `sync.RWMutex`:
  - Readers (`Get`, `GetN`) → `RLock`
  - Writers (`AddNode`, `RemoveNode`, `UpdateWeight`) → full `Lock`
- Ring slice rebuilt atomically under write lock
- Supports high-concurrency workloads

---

## Node Addition and Removal

### Adding a Node
1. Generate vnode hashes:
hash(nodeID + "#" + index)
2. Append to ring and sort
3. Update mapping:
nodeToHashes[nodeID] = [hash0, hash1, ...]

### Removing a Node
1. Retrieve vnode hashes: `nodeToHashes[nodeID]`
2. Filter ring to remove vnodes
3. Delete `nodeToHashes[nodeID]`
- Only keys of removed vnodes are reassigned
- Minimal key movement ensures stability

---

## Example Data Structures

```go
type VNode struct {
    Hash   uint64
    NodeID string
}

var ring []VNode
var nodeToHashes map[string][]uint64