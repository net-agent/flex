package packet

import (
	"reflect"
	"testing"
)

func TestBuffer_SetOpenACK(t *testing.T) {
	type args struct {
		msg string
	}
	tests := []struct {
		name string
		buf  *Buffer
		args args
		want *Buffer
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.buf.SetOpenACK(tt.args.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Buffer.SetOpenACK() = %v, want %v", got, tt.want)
			}
		})
	}
}
