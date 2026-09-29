# Packet Package Benchmark (Before Optimization)

**Environment**: darwin/arm64, Apple M4, Go

## Results (3 runs, benchtime=2s)

### Buffer Allocation
| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| NewBuffer | 0.23 | 0 | 0 |

> Note: compiler escape analysis keeps Buffer on stack when it doesn't escape.

### ReadWriteBuffer (full read/write cycle over net.Pipe)
| Payload Size | ns/op | MB/s | B/op | allocs/op |
|-------------|-------|------|------|-----------|
| NoPayload (11B) | 647 | 17.0 | 64 | 3 |
| Small (64B) | 1252 | 59.9 | 128 | 4 |
| Medium (1KB) | 1317 | 785.9 | 1088 | 4 |
| Large (16KB) | 2352 | 6971.2 | 16448 | 4 |
| Max (64KB) | 5296 | 12377.0 | 65601 | 4 |

> 3 allocs for no-payload (Buffer+Header on each side), 4 allocs with payload (+make([]byte, sz) on read side).

### WriteTo (to devNull, no syscall overhead)
| Payload Size | ns/op | MB/s | B/op | allocs/op |
|-------------|-------|------|------|-----------|
| NoPayload | 1.57 | 7,000 | 0 | 0 |
| Small (64B) | 2.32 | 32,330 | 0 | 0 |
| Large (16KB) | 2.32 | 7,073,029 | 0 | 0 |

### Header Operations
| Benchmark | ns/op | allocs/op |
|-----------|-------|-----------|
| HeaderSetGet | 0.81 | 0 |
| SwapSrcDist | 0.83 | 0 |

## Key Observations

1. **ReadWriteBuffer is the hot path** — 4 allocs per packet (Buffer, Header, payload on each side)
2. **WriteTo** itself is extremely fast; real overhead comes from syscalls (two Write calls per packet)
3. **Header operations** are fully inlined, sub-nanosecond — no optimization needed
4. **Payload allocation** dominates memory: 16KB payload = 16448 B/op
