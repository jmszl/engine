package track

import (
	"context"
	"testing"
	"time"

	"m7s.live/engine/v4/util"
	. "m7s.live/engine/v4/common"
)

func TestRing(t *testing.T) {
	w := &util.RingWriter[any, *AVFrame]{}
	w.Init(10,func() *AVFrame {
		return &AVFrame{}
	})
	ctx, _ := context.WithTimeout(context.Background(), time.Second*10)
	go t.Run("writer", func(t *testing.T) {
		for i := 0; ctx.Err() == nil; i++ {
			w.Value.Data = i
			normal := w.Step()
			t.Log("write", i, normal)
			time.Sleep(time.Millisecond * 50)
		}
	})
	go t.Run("reader1", func(t *testing.T) {
		var reader RingReader[any, *AVFrame]
		err := reader.StartRead(w.Ring)
		if err != nil {
			t.Error(err)
			return
		}
		for ctx.Err() == nil {
			err = reader.ReadNext()
			t.Log("read1", reader.Value.Data)
			if err != nil {
				t.Error(err)
				break
			}
			time.Sleep(time.Millisecond * 10)
		}
		reader.StopRead()
		<-ctx.Done()
	})
	// slow reader
	t.Run("reader2", func(t *testing.T) {
		var reader RingReader[any, *AVFrame]
		err := reader.StartRead(w.Ring)
		if err != nil {
			t.Error(err)
			return
		}
		for ctx.Err() == nil {
			err = reader.ReadNext()
			if err != nil {
				// t.Error(err)
				return
			}
			t.Log("read2", reader.Value.Data)
			time.Sleep(time.Millisecond * 100)
		}
		reader.StopRead()
		<-ctx.Done()
	})
}
