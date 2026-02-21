# Packet Package Benchmark (After Optimization)

**Environment**: darwin/arm64, Apple M4, Go

## Optimizations Applied

1. `Header` changed from pointer (`*Header`) to value type — eliminates 1 heap allocation per Buffer
2. `sync.Pool` added for Buffer objects (`GetBuffer`/`PutBuffer`) — eliminates Buffer allocation in hot read path
3. `NewBuffer()` simplified (no parameter) — all callers passed nil

## Results (3 runs, benchtime=2s)

### Buffer Allocation
| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| NewBuffer | 0.23 | 0 | 0 |
| GetPutBuffer (pool) | 7.16 | 0 | 0 |

### ReadWriteBuffer — NewBuffer (no pool, baseline comparison)
| Payload Size | ns/op | MB/s | B/op | allocs/op |
|-------------|-------|------|------|-----------|
| NoPayload (11B) | 645 | 17.1 | 96 | 2 |
| Small (64B) | 1227 | 61.1 | 160 | 3 |
| Medium (1KB) | 1286 | 804.8 | 1120 | 3 |
| Large (16KB) | 2349 | 6980 | 16486 | 3 |
| Max (64KB) | 5268 | 12443 | 65663 | 3 |

### ReadWriteBufferPool — GetBuffer + PutBuffer (recommended hot path)
| Payload Size | ns/op | MB/s | B/op | allocs/op |
|-------------|-------|------|------|-----------|
| NoPayload (11B) | 575 | 19.1 | 0 | **0** |
| Small (64B) | 1160 | 64.7 | 64 | **1** |
| Medium (1KB) | 1235 | 838.2 | 1024 | **1** |
| Large (16KB) | 2291 | 7156 | 16390 | **1** |
| Max (64KB) | 5207 | 12589 | 65568 | **1** |

### WriteTo (to devNull)
| Payload Size | ns/op | MB/s | B/op | allocs/op |
|-------------|-------|------|------|-----------|
| NoPayload | 1.82 | 6,043 | 0 | 0 |
| Small (64B) | 2.51 | 29,857 | 0 | 0 |
| Large (16KB) | 2.51 | 6,521,314 | 0 | 0 |

### Header Operations
| Benchmark | ns/op | allocs/op |
|-----------|-------|-----------|
| HeaderSetGet | 0.25 | 0 |
| SwapSrcDist | 0.89 | 0 |

## Comparison: Before vs After (Pool Path)

| Payload Size | Old allocs | New allocs | Old ns/op | New ns/op | Speedup |
|-------------|-----------|-----------|-----------|-----------|---------|
| NoPayload | 3 | **0** | 647 | 575 | 11% faster |
| Small (64B) | 4 | **1** | 1252 | 1160 | 7% faster |
| Medium (1KB) | 4 | **1** | 1317 | 1235 | 6% faster |
| Large (16KB) | 4 | **1** | 2352 | 2291 | 3% faster |
| Max (64KB) | 4 | **1** | 5296 | 5207 | 2% faster |

## Key Improvements

1. **Zero-alloc for control packets** (no payload): 3 allocs → 0 allocs, 11% faster
2. **Single alloc for data packets**: 4 allocs → 1 alloc (only the payload `make([]byte, sz)` remains)
3. **HeaderSetGet 3x faster**: 0.81 ns → 0.25 ns (value-type Header enables better inlining)
4. **GC pressure significantly reduced**: in high-throughput scenarios, the pool eliminates most short-lived Buffer allocations
