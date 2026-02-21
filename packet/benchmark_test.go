package packet

import (
	"net"
	"sync"
	"testing"
)

// BenchmarkNewBuffer measures Buffer+Header allocation cost.
func BenchmarkNewBuffer(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewBuffer()
	}
}

// BenchmarkReadWriteBuffer measures a full read/write cycle over net.Pipe.
func BenchmarkReadWriteBuffer(b *testing.B) {
	payloads := []struct {
		name string
		size int
	}{
		{"NoPayload", 0},
		{"Small_64B", 64},
		{"Medium_1KB", 1024},
		{"Large_16KB", 16384},
		{"Max_64KB", 65535},
	}

	for _, p := range payloads {
		b.Run(p.name, func(b *testing.B) {
			c1, c2 := net.Pipe()
			defer c1.Close()
			defer c2.Close()

			w := NewConnWriter(c1)
			r := NewConnReader(c2)

			payload := make([]byte, p.size)

			b.ReportAllocs()
			b.SetBytes(int64(HeaderSz + p.size))
			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < b.N; i++ {
					buf := NewBuffer()
					buf.SetCmd(CmdPushStreamData)
					if p.size > 0 {
						buf.SetPayload(payload)
					}
					w.WriteBuffer(buf)
				}
			}()

			for i := 0; i < b.N; i++ {
				_, err := r.ReadBuffer()
				if err != nil {
					b.Fatal(err)
				}
			}
			wg.Wait()
		})
	}
}

// BenchmarkWriteTo measures WriteTo performance with an io.Discard-like writer.
func BenchmarkWriteTo(b *testing.B) {
	payloads := []struct {
		name string
		size int
	}{
		{"NoPayload", 0},
		{"Small_64B", 64},
		{"Large_16KB", 16384},
	}

	for _, p := range payloads {
		b.Run(p.name, func(b *testing.B) {
			buf := NewBuffer()
			buf.SetCmd(CmdPushStreamData)
			if p.size > 0 {
				buf.SetPayload(make([]byte, p.size))
			}

			w := devNull{}
			b.ReportAllocs()
			b.SetBytes(int64(HeaderSz + p.size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				buf.WriteTo(w)
			}
		})
	}
}

// BenchmarkHeaderSetGet measures header field access performance.
func BenchmarkHeaderSetGet(b *testing.B) {
	buf := NewBuffer()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.SetHeader(CmdPushStreamData, 100, 200, 300, 400)
		_ = buf.Cmd()
		_ = buf.DistIP()
		_ = buf.DistPort()
		_ = buf.SrcIP()
		_ = buf.SrcPort()
		_ = buf.SID()
	}
}

// BenchmarkSwapSrcDist measures SwapSrcDist performance.
func BenchmarkSwapSrcDist(b *testing.B) {
	buf := NewBuffer()
	buf.SetHeader(CmdPushStreamData, 1, 2, 3, 4)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.SwapSrcDist()
	}
}

// BenchmarkGetPutBuffer measures pool-based Buffer allocation.
func BenchmarkGetPutBuffer(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := GetBuffer()
		PutBuffer(buf)
	}
}

// BenchmarkReadWriteBufferPool measures read/write with pool (reader uses GetBuffer internally).
func BenchmarkReadWriteBufferPool(b *testing.B) {
	payloads := []struct {
		name string
		size int
	}{
		{"NoPayload", 0},
		{"Small_64B", 64},
		{"Medium_1KB", 1024},
		{"Large_16KB", 16384},
		{"Max_64KB", 65535},
	}

	for _, p := range payloads {
		b.Run(p.name, func(b *testing.B) {
			c1, c2 := net.Pipe()
			defer c1.Close()
			defer c2.Close()

			w := NewConnWriter(c1)
			r := NewConnReader(c2)

			payload := make([]byte, p.size)

			// Writer reuses a single buffer (like Sender pattern)
			writeBuf := NewBuffer()
			writeBuf.SetCmd(CmdPushStreamData)

			b.ReportAllocs()
			b.SetBytes(int64(HeaderSz + p.size))
			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < b.N; i++ {
					if p.size > 0 {
						writeBuf.SetPayload(payload)
					}
					w.WriteBuffer(writeBuf)
				}
			}()

			for i := 0; i < b.N; i++ {
				buf, err := r.ReadBuffer()
				if err != nil {
					b.Fatal(err)
				}
				PutBuffer(buf)
			}
			wg.Wait()
		})
	}
}

// devNull implements io.Writer, discards all data.
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }
