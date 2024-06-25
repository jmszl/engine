package log

import (
	"io"
	"sync"
)

type MultipleWriter struct {
	sync.Map // 用于存储多个输出
}

func (m *MultipleWriter) Write(p []byte) (n int, err error) {
	m.Range(func(key, value any) bool {
		if _, err := key.(io.Writer).Write(p); err != nil {
			m.Delete(key)
		}
		return true
	})
	return
}

func (m *MultipleWriter) Add(writer io.Writer) {
	m.Map.Store(writer, struct{}{})
}

var multipleWriter = &MultipleWriter{}

func AddWriter(writer io.Writer) {
	multipleWriter.Add(writer)
}
func DeleteWriter(writer io.Writer) {
	multipleWriter.Delete(writer)
}
