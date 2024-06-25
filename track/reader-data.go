package track

import (
	"m7s.live/engine/v4/common"
	"m7s.live/engine/v4/util"
)

type RingReader[T any, F util.IDataFrame[T]] struct {
	*util.Ring[F]
	locked bool
	Count int // 读取的帧数
}

func (r *RingReader[T, F]) StartRead(ring *util.Ring[F]) (err error) {
	r.Ring = ring
	if r.Value.IsDiscarded() {
		return ErrDiscard
	}
	r.Value.ReaderEnter()
	r.locked = true
	r.Count++
	return
}

func (r *RingReader[T, F]) TryRead() (f F, err error) {
	if r.Count > 0 {
		preValue := r.Value
		if preValue.IsDiscarded() {
			preValue.ReaderLeave()
			err = ErrDiscard
			return
		}
		if !r.Next().Value.ReaderTryEnter() {
			return
		}
		r.Ring = r.Next()
	} else {
		if !r.Value.ReaderTryEnter()  {
			return
		}
	}
	if r.Value.IsDiscarded() {
		err = ErrDiscard
		return
	}
	r.Count++
	f = r.Value
	return
}

func (r *RingReader[T, F]) StopRead() {
	if r.locked {
		r.Value.ReaderLeave()
		r.locked = false
	}
}

func (r *RingReader[T, F]) ReadNext() (err error) {
	return r.Read(r.Next())
}

func (r *RingReader[T, F]) Read(ring *util.Ring[F]) (err error) {
	r.StopRead()
	return r.StartRead(ring)
}

type DataReader[T any] struct {
	RingReader[T, *common.DataFrame[T]]
}

