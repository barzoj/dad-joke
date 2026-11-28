package ipc

import (
	"bytes"
	"testing"
)

func FuzzReadFrame(f *testing.F) {
	var good bytes.Buffer
	_ = WriteFrame(&good, []byte(`{"type":"hello","protocol":1}`))
	f.Add(good.Bytes())
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0, 0, 1, 0}) // len=256, truncated
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ReadFrame(bytes.NewReader(data))
	})
}

func FuzzDecodeMessage(f *testing.F) {
	f.Add([]byte(`{"type":"hello","protocol":1,"agent_version":"x","session_id":1,"pid":2}`))
	f.Add([]byte(`{"type":"msg","msg_type":"x","payload":{}}`))
	f.Add([]byte(`{"type":"reject","code":"version_mismatch"}`))
	f.Add([]byte("{"))
	f.Add([]byte("\xff\xfe"))
	f.Add([]byte(`{"type":"hello","protocol":999999999999999999999}`))
	f.Add([]byte(`{"type":"x","nested":` + nest(40) + `}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeMessage(data)
	})
}

func nest(n int) string {
	s := `{"a":1}`
	for i := 0; i < n; i++ {
		s = `{"k":` + s + `}`
	}
	return s
}
