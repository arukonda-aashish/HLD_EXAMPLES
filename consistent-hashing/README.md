# Consistent Hashing Library

This library implements a production-ready **consistent hashing** system with support for virtual nodes, weighted capacity, replication, bounded-load, node status management, and pluggable hash functions. The design is inspired by real-world systems like Apache Cassandra, Amazon DynamoDB, and Redis Cluster.

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

- The ring is represented as a **sorted slice of virtual nodes**, each holding a `uint64` hash and reference to its physical node.
- Lookup of a key is **O(log N)** using **binary search**, returning the first vnode whose hash ≥ `hash(key)`.
- The ring simulates circular behavior: if no vnode ≥ key hash is found, lookup wraps around to the first vnode.

---

## Virtual Nodes and Weighted Capacity

- Each physical node is assigned **BaseVirtualNodes × Weight** virtual nodes.  
- Example: A node with `Weight = 3` gets 3× the number of vnodes, thus ~3× the key share.
- Virtual nodes are hashed independently:
  ```text
  hash(nodeID + "#" + index)